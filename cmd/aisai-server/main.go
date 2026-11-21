// Command aisai-server is the AISAI backend.
//
// It listens on a Unix domain socket (or a named pipe on Windows) so the API
// is never exposed to the network. On startup it prints exactly one line of
// JSON describing the listen address, which the Electron main process
// parses; everything else goes to stderr.
//
//	go run ./cmd/aisai-server --socket /tmp/aisai.sock
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/tlmcguire/aisai/internal/advisory"
	"github.com/tlmcguire/aisai/internal/audit"
	"github.com/tlmcguire/aisai/internal/chat"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/deps"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/index"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/patch"
	"github.com/tlmcguire/aisai/internal/pypi"
	"github.com/tlmcguire/aisai/internal/sast"
	"github.com/tlmcguire/aisai/internal/scan"
	"github.com/tlmcguire/aisai/internal/server"
	"github.com/tlmcguire/aisai/internal/version"
)

func main() {
	var (
		socketPath = flag.String("socket", "", "path of the Unix socket to listen on")
		cfgPath    = flag.String("config", "", "path of the config file")
		workspace  = flag.String("workspace", "", "override the workspace directory")
		showVer    = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("aisai: ")

	if *showVer {
		fmt.Println(version.String())
		return
	}

	if err := run(*socketPath, *cfgPath, *workspace); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run(socketPath, cfgPath, workspaceOverride string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if workspaceOverride != "" {
		cfg.Workspace = workspaceOverride
	}
	if socketPath != "" {
		cfg.SocketPath = socketPath
	}
	if err := cfg.EnsureWorkspace(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(cfg.Workspace, "audits"), 0o755); err != nil {
		return err
	}

	// --- stores -----------------------------------------------------------
	store, err := findings.Open(filepath.Join(cfg.Workspace, "findings.sqlite"))
	if err != nil {
		return err
	}
	defer store.Close()

	idx, err := index.Open(filepath.Join(cfg.Workspace, "index.sqlite"))
	if err != nil {
		return err
	}
	defer idx.Close()

	// --- scanners ---------------------------------------------------------
	pypiClient := pypi.NewClient(pypi.ClientOptions{UserAgent: "AISAI/" + version.Short()})
	resolver := deps.NewResolver(deps.Options{PyPI: pypiClient})
	engine := sast.NewEngine()

	// --- LLM provider -----------------------------------------------------
	//
	// The model set is shared by the provider, the detection pipeline and
	// the chat. It is mutable on purpose: the desktop app's model picker
	// changes the models through the API, and every consumer reads them
	// from here on each request.
	models := llm.NewModels(cfg.ChatModel, cfg.ScanModel, cfg.EmbedModel, cfg.NumCtx, cfg.KeepAlive)

	var provider llm.Provider
	if !cfg.DisableLLM {
		if cfg.BYOKBaseURL != "" && cfg.BYOKAPIKey != "" {
			provider = llm.NewOpenAICompat(llm.Options{
				URL:     cfg.BYOKBaseURL,
				Model:   cfg.BYOKModel,
				APIKey:  cfg.BYOKAPIKey,
				NumCtx:  cfg.NumCtx,
				Timeout: 180 * time.Second,
			})
			log.Printf("using BYOK provider at %s (model %s)", cfg.BYOKBaseURL, cfg.BYOKModel)
		} else {
			provider = llm.NewOllama(llm.Options{
				URL:        strings.Join(cfg.OllamaURLs, ","),
				Model:      cfg.ChatModel,
				EmbedModel: cfg.EmbedModel,
				NumCtx:     cfg.NumCtx,
				KeepAlive:  cfg.KeepAlive,
				Models:     models,
				Timeout:    180 * time.Second,
			})
			log.Printf("using Ollama at %s (model %s, keep-alive %s)",
				strings.Join(cfg.OllamaURLs, ","), cfg.ChatModel, cfg.KeepAlive)
		}
	} else {
		log.Printf("LLM features are disabled (DISABLE_LLM)")
	}

	// --- advisory ---------------------------------------------------------
	adv := advisory.New(advisory.Options{
		OfflineDBPath: cfg.OSVDBPath,
		UserAgent:     "AISAI/" + version.Short(),
	})
	if adv.Offline() {
		log.Printf("advisory layer is offline (database at %s)", cfg.OSVDBPath)
	}

	// --- audit manager ----------------------------------------------------
	srv := server.New(server.Options{
		Config:  cfg,
		Store:   store,
		LLM:     provider,
		Models:  models,
		Index:   idx,
		Patches: patch.NewApplier(),
		PyPI:    pypiClient,
	})

	// The LLM detector is only built when a provider is available.
	var detector *scan.Detector
	if provider != nil {
		detector = scan.NewDetectorWithModels(provider, models, cfg.NumCtx)
	}

	mgr := audit.NewManager(audit.Deps{
		Config:   cfg,
		PyPI:     pypiClient,
		Resolver: resolver,
		SAST:     engine,
		Store:    store,
		Index:    idx,
		LLM:      provider,
		Detector: detector,
		Advisory: adv,
		Emit: func(ev model.Event) {
			srv.Hub().Broadcast(ev)
		},
	})
	srv.SetManager(mgr)

	// The chat's tools are scoped to one audit, so a chat engine is built
	// per request.
	srv.SetChatFactory(func(auditID string) server.ChatEngine {
		tools := audit.NewTools(mgr, auditID)
		return server.NewChatEngine(chat.New(chat.Deps{
			Index:      idx,
			LLM:        provider,
			Models:     models,
			NumCtx:     cfg.NumCtx,
			Tools:      audit.ToolDefinitions(),
			ToolRunner: tools,
		}))
	})

	// --- listener ---------------------------------------------------------
	addr, listener, err := listen(cfg)
	if err != nil {
		return err
	}
	defer listener.Close()

	// The Electron main process parses this single line.
	if err := emitListening(addr); err != nil {
		return err
	}

	httpServer := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// No write timeout: the WebSocket and streaming chat responses are
		// long-lived by design.
	}

	// --- serve ------------------------------------------------------------
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	srv.Hub().Close()
	return nil
}

// listen opens the API socket.
//
// A Unix domain socket is preferred: it keeps the API off the network
// entirely. If the platform does not support one, or the path is unusable,
// the server falls back to a loopback TCP port.
func listen(cfg *config.Config) (string, net.Listener, error) {
	if runtime.GOOS != "windows" {
		path := cfg.SocketPath
		if path == "" {
			path = filepath.Join(os.TempDir(), fmt.Sprintf("aisai-%d.sock", os.Getuid()))
		}
		// Remove a stale socket left by a previous crashed run.
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			_ = os.Remove(path)
		}
		l, err := net.Listen("unix", path)
		if err == nil {
			// The socket is the API's only entry point, so it must not be
			// reachable by other users on the machine. The default umask
			// would leave it world-accessible on a shared host.
			if err := os.Chmod(path, 0o600); err != nil {
				log.Printf("could not restrict the socket permissions: %v", err)
			}
			return path, l, nil
		}
		log.Printf("unix socket unavailable (%v), falling back to loopback TCP", err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	return l.Addr().String(), l, nil
}

// emitListening prints the single JSON line the Electron main process reads.
func emitListening(addr string) error {
	payload := map[string]any{
		"type":      "listening",
		"transport": "unix",
		"address":   addr,
		"version":   version.Short(),
	}
	if strings.Contains(addr, ":") && !strings.Contains(addr, "/") {
		payload["transport"] = "tcp"
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	// Flush stdout: the parent process is reading this line.
	if f, ok := any(os.Stdout).(*os.File); ok {
		_ = f.Sync()
	}
	return nil
}

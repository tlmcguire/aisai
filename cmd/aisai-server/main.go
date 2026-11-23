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
	"io"
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
	"github.com/tlmcguire/aisai/internal/registry"
	"github.com/tlmcguire/aisai/internal/sast"
	"github.com/tlmcguire/aisai/internal/scan"
	"github.com/tlmcguire/aisai/internal/server"
	"github.com/tlmcguire/aisai/internal/version"
	"github.com/tlmcguire/aisai/internal/websearch"
)

func main() {
	var (
		socketPath = flag.String("socket", "", "path of the Unix socket to listen on")
		cfgPath    = flag.String("config", "", "path of the config file")
		workspace  = flag.String("workspace", "", "override the workspace directory")
		showVer    = flag.Bool("version", false, "print the version and exit")
		withParent = flag.Bool("exit-with-parent", false, "shut down when stdin closes (the parent process died)")
	)
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("aisai: ")

	if *showVer {
		fmt.Println(version.String())
		return
	}

	if err := run(*socketPath, *cfgPath, *workspace, *withParent); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run(socketPath, cfgPath, workspaceOverride string, exitWithParent bool) error {
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
	agent := "AISAI/" + version.Short()
	registries := registry.NewSet(
		registry.NewPyPI(pypiClient),
		registry.NewNPM(registry.NPMOptions{UserAgent: agent}),
		registry.NewCrates(registry.CratesOptions{UserAgent: agent + " (+https://github.com/tlmcguire/aisai)"}),
	)
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
		kind := cfg.ProviderKind
		if kind == "" && cfg.BYOKBaseURL != "" && cfg.BYOKAPIKey != "" {
			kind = llm.KindOpenAI
		}
		if kind == "" {
			kind = llm.KindOllama
		}
		var inner llm.Provider
		var url string
		if kind != llm.KindOllama && cfg.BYOKBaseURL != "" {
			url = cfg.BYOKBaseURL
			inner = llm.NewOpenAICompat(llm.Options{
				URL:     cfg.BYOKBaseURL,
				Model:   cfg.BYOKModel,
				APIKey:  cfg.BYOKAPIKey,
				Headers: llm.HostedHeaders(kind, cfg.BYOKAPIKey),
				NumCtx:  cfg.NumCtx,
				Models:  models,
				Timeout: 180 * time.Second,
			})
			log.Printf("using %s provider at %s (model %s)", kind, cfg.BYOKBaseURL, cfg.BYOKModel)
		} else {
			kind = llm.KindOllama
			url = strings.Join(cfg.OllamaURLs, ",")
			inner = llm.NewOllama(llm.Options{
				URL:        url,
				Model:      cfg.ChatModel,
				EmbedModel: cfg.EmbedModel,
				NumCtx:     cfg.NumCtx,
				KeepAlive:  cfg.KeepAlive,
				Models:     models,
				Timeout:    180 * time.Second,
			})
			log.Printf("using Ollama at %s (model %s, keep-alive %s)", url, cfg.ChatModel, cfg.KeepAlive)
		}
		// Wrapped so the desktop app can switch backends without a restart.
		provider = llm.NewSwitchable(inner, kind, url)
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
		Config:     cfg,
		Store:      store,
		LLM:        provider,
		Models:     models,
		Index:      idx,
		Patches:    patch.NewApplier(),
		PyPI:       pypiClient,
		Registries: registries,
	})

	// The LLM detector is only built when a provider is available.
	var detector *scan.Detector
	if provider != nil {
		detector = scan.NewDetectorWithModels(provider, models, cfg.NumCtx)
	}

	mgr := audit.NewManager(audit.Deps{
		Config:     cfg,
		PyPI:       pypiClient,
		Registries: registries,
		Resolver:   resolver,
		SAST:       engine,
		Store:      store,
		Index:      idx,
		LLM:        provider,
		Detector:   detector,
		Advisory:   adv,
		Emit: func(ev model.Event) {
			srv.Hub().Broadcast(ev)
		},
	})
	srv.SetManager(mgr)

	// The chat's tools are scoped to one audit, so a chat engine is built
	// per request.
	srv.SetChatFactory(func(auditID string) server.ChatEngine {
		var web *websearch.Client
		if cfg.WebSearch {
			web = &websearch.Client{}
		}
		tools, err := audit.NewChatTools(mgr, auditID, web)
		if err != nil {
			return nil
		}
		return server.NewChatEngine(chat.New(chat.Deps{
			Index:      idx,
			LLM:        provider,
			Models:     models,
			NumCtx:     cfg.NumCtx,
			Tools:      audit.ChatToolDefinitions(web != nil),
			ToolRunner: tools,
		}))
	})

	// --- listener ---------------------------------------------------------
	addr, listener, err := listen(cfg)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer srv.CloseTerminals()     // no shell outlives the server
	defer srv.CloseDebugSessions() // nor a debugged program

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

	// The desktop app holds the write end of our stdin. When it exits for any
	// reason (quit, crash, SIGKILL) the pipe closes, and without this the
	// server would be adopted by init and linger forever.
	if exitWithParent {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			sigCh <- syscall.SIGHUP
		}()
		// Belt and braces: if the pipe somehow stays open, a parent that has
		// died still shows as a changed parent PID (we are adopted by init).
		parent := os.Getppid()
		go func() {
			for range time.Tick(2 * time.Second) {
				if os.Getppid() != parent {
					sigCh <- syscall.SIGHUP
					return
				}
			}
		}()
	}

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
// the server falls back to a loopback TCP port, which is always free.
//
// The socket path is shared by every copy of AISAI on the machine, so it can
// be taken already. When it is, the server takes a socket of its own instead
// of the one that is in use: the copy that owns it is still serving, and
// taking its path would leave it unable to answer.
func listen(cfg *config.Config) (string, net.Listener, error) {
	if runtime.GOOS != "windows" {
		path := cfg.SocketPath
		if path == "" {
			path = filepath.Join(os.TempDir(), fmt.Sprintf("aisai-%d.sock", os.Getuid()))
		}
		l, err := listenUnix(path)
		if err == nil {
			// The socket is the API's only entry point, so it must not be
			// reachable by other users on the machine. The default umask
			// would leave it world-accessible on a shared host.
			if err := os.Chmod(path, 0o600); err != nil {
				log.Printf("could not restrict the socket permissions: %v", err)
			}
			return path, l, nil
		}
		log.Printf("the socket %s is not available (%v), taking one of our own", path, err)
		own := filepath.Join(filepath.Dir(path), fmt.Sprintf("aisai-%d-%d.sock", os.Getuid(), os.Getpid()))
		l, ownErr := listenUnix(own)
		if ownErr == nil {
			if err := os.Chmod(own, 0o600); err != nil {
				log.Printf("could not restrict the socket permissions: %v", err)
			}
			log.Printf("listening on %s", own)
			return own, l, nil
		}
		log.Printf("could not use %s either (%v), falling back to loopback TCP", own, ownErr)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	return l.Addr().String(), l, nil
}

// listenUnix listens on a Unix socket.
//
// A socket left behind by a crashed run is removed and taken over, because
// nobody answers on it. A socket that another copy of AISAI is listening on
// is left alone: that one is still serving.
func listenUnix(path string) (net.Listener, error) {
	l, err := net.Listen("unix", path)
	if err == nil {
		return l, nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		return nil, err
	}
	// The path is taken: is anyone answering on it?
	if conn, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond); dialErr == nil {
		conn.Close()
		return nil, err
	}
	// Nobody answers, so the socket is a leftover: remove it and try again.
	if rmErr := os.Remove(path); rmErr != nil {
		return nil, err
	}
	return net.Listen("unix", path)
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

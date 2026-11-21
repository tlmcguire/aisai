// Command aisai-mcp exposes the AISAI backend to external agents over the
// Model Context Protocol.
//
// The server speaks JSON-RPC 2.0 over stdin and stdout. STDOUT IS THE
// PROTOCOL CHANNEL: every log line, warning and diagnostic goes to stderr,
// because a stray write to stdout corrupts the JSON-RPC stream and makes the
// client drop the connection.
//
//	go run ./cmd/aisai-mcp --workspace /tmp/aisai
//
// The object graph is the same one the HTTP server builds: config, stores,
// PyPI client, dependency resolver, SAST engine, LLM provider, advisory
// client, audit manager, chat engine, and finally the MCP tool set.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
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
	"github.com/tlmcguire/aisai/internal/mcp"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/patch"
	"github.com/tlmcguire/aisai/internal/pypi"
	"github.com/tlmcguire/aisai/internal/sast"
	"github.com/tlmcguire/aisai/internal/scan"
	"github.com/tlmcguire/aisai/internal/version"
)

func main() {
	var (
		workspace = flag.String("workspace", "", "override the workspace directory")
		dbPath    = flag.String("db", "", "path of the findings database (default: <workspace>/findings.sqlite)")
		idxPath   = flag.String("index", "", "path of the retrieval index (default: <workspace>/index.sqlite)")
		cfgPath   = flag.String("config", "", "path of the config file")
		socket    = flag.String("socket", "", "optional socket path (recorded only: the MCP transport is stdio)")
		address   = flag.String("address", "", "optional address (recorded only: the MCP transport is stdio)")
	)
	flag.Parse()

	// Everything the process logs goes to stderr: stdout carries the
	// JSON-RPC protocol and nothing else.
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("aisai-mcp: ")
	log.SetOutput(os.Stderr)

	if err := run(*workspace, *dbPath, *idxPath, *cfgPath, *socket, *address); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

// run builds the object graph and serves the MCP protocol on stdin/stdout.
func run(workspaceOverride, dbPath, idxPath, cfgPath, socketPath, address string) error {
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
	storePath := dbPath
	if storePath == "" {
		storePath = filepath.Join(cfg.Workspace, "findings.sqlite")
	}
	store, err := findings.Open(storePath)
	if err != nil {
		return err
	}
	defer store.Close()

	indexPath := idxPath
	if indexPath == "" {
		indexPath = filepath.Join(cfg.Workspace, "index.sqlite")
	}
	idx, err := index.Open(indexPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	// --- scanners ---------------------------------------------------------
	pypiClient := pypi.NewClient(pypi.ClientOptions{UserAgent: "AISAI/" + version.Short()})
	resolver := deps.NewResolver(deps.Options{PyPI: pypiClient})
	engine := sast.NewEngine()

	// --- LLM provider -----------------------------------------------------
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
				Timeout:    180 * time.Second,
			})
			log.Printf("using Ollama at %s (model %s)", strings.Join(cfg.OllamaURLs, ","), cfg.ChatModel)
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
	var detector *scan.Detector
	if provider != nil {
		detector = scan.NewDetector(provider, cfg.ScanModel, cfg.NumCtx)
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
			log.Printf("event %s %s", ev.Type, ev.Message)
		},
	})

	// --- MCP tool set -----------------------------------------------------
	toolset := mcp.NewToolset(mcp.Deps{
		Manager:   mgr,
		Advisory:  adv,
		Index:     idx,
		Store:     store,
		LLM:       provider,
		ChatModel: cfg.ChatModel,
		NumCtx:    cfg.NumCtx,
		Patches:   patch.NewApplier(),
	})

	// The chat's tools are scoped to one audit; the toolset routes them to the
	// current audit.
	toolset.SetChat(chat.New(chat.Deps{
		Index:      idx,
		LLM:        provider,
		Model:      cfg.ChatModel,
		NumCtx:     cfg.NumCtx,
		Tools:      audit.ToolDefinitions(),
		ToolRunner: toolset,
	}))

	// --- server -----------------------------------------------------------
	srv := mcp.NewServer()
	mcp.RegisterTools(srv, toolset)

	if socketPath != "" || address != "" {
		log.Printf("note: --socket/--address are recorded only; the MCP transport is stdio")
	}
	// The banner goes to stderr so that stdout stays pure protocol.
	log.Printf("aisai-mcp ready (workspace=%s, version=%s)", cfg.Workspace, version.Short())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx, os.Stdin, os.Stdout)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
		cancel()
		// Give the server a moment to finish the request in flight.
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
		}
		return nil
	}
}

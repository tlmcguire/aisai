// Package server exposes the AISAI backend over HTTP and WebSocket.
//
// The server listens on a Unix domain socket (or a named pipe on Windows) so
// the API is never reachable from the network. The legacy implementation ran
// a Flask debug server bound to 0.0.0.0, which exposed an interactive
// debugger on every published port.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/audit"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/index"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/patch"
	"github.com/tlmcguire/aisai/internal/sbom"
	"github.com/tlmcguire/aisai/internal/scan"
	"github.com/tlmcguire/aisai/internal/version"
)

// Server wires the HTTP routes to the audit manager and the stores.
type Server struct {
	cfg         *config.Config
	hub         *Hub
	manager     *audit.Manager
	store       *findings.Store
	llm         llm.Provider
	index       IndexStore
	chat        ChatEngine
	chatFactory ChatFactory
	patches     PatchApplier
	started     time.Time
}

// IndexStore is the subset of the index API the server needs.
type IndexStore interface {
	AddChunks(auditID string, chunks []model.Chunk) error
	Status(auditID string) (index.Status, error)
	Embed(ctx context.Context, auditID string, provider llm.Provider, model string, progress func(done, total int)) error
}

// ChatEngine is the subset of the chat API the server needs.
type ChatEngine interface {
	Ask(ctx context.Context, req ChatRequest, emit func(token string)) (*ChatResult, error)
}

// ChatFactory builds a chat engine bound to one audit.
//
// The chat's tool runner needs the audit id, which is only known per
// request, so the engine is built per audit rather than once at startup.
type ChatFactory func(auditID string) ChatEngine

// ChatRequest is a chat turn.
type ChatRequest struct {
	AuditID  string
	Question string
	History  []llm.Message
}

// ChatResult is the outcome of a chat turn.
type ChatResult struct {
	Answer     string
	Citations  []ChatCitation
	ChunksUsed []model.Chunk
}

// ChatCitation is a file:line reference produced by the chat.
type ChatCitation struct {
	File   string
	Line   int
	Symbol string
}

// PatchApplier applies a unified diff inside an audit workspace.
type PatchApplier interface {
	Apply(ctx context.Context, root, path, unified string) error
	Changes(ctx context.Context, root string) ([]patch.AppliedPatch, error)
	// Revert restores the pre-patch contents of a file. It reports whether
	// there was a change to revert.
	Revert(ctx context.Context, root, path string) (bool, error)
	// PatchSeries renders every un-reverted change as one unified diff.
	PatchSeries(ctx context.Context, root string) (string, error)
}

// Options configures a Server.
type Options struct {
	Config      *config.Config
	Manager     *audit.Manager
	Store       *findings.Store
	LLM         llm.Provider
	Index       IndexStore
	Chat        ChatEngine
	ChatFactory ChatFactory
	Patches     PatchApplier
}

// New creates a server.
func New(opts Options) *Server {
	s := &Server{
		cfg:         opts.Config,
		hub:         NewHub(),
		manager:     opts.Manager,
		store:       opts.Store,
		llm:         opts.LLM,
		index:       opts.Index,
		chat:        opts.Chat,
		chatFactory: opts.ChatFactory,
		patches:     opts.Patches,
		started:     time.Now(),
	}
	return s
}

// Hub exposes the event hub so the manager can emit progress.
func (s *Server) Hub() *Hub { return s.hub }

// SetManager attaches the audit manager.
//
// The manager is created after the server because the manager's event
// emitter needs the server's hub.
func (s *Server) SetManager(m *audit.Manager) { s.manager = m }

// SetChatFactory installs the per-audit chat engine factory.
func (s *Server) SetChatFactory(f ChatFactory) { s.chatFactory = f }

// Handler returns the configured HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/providers", s.handleProviders)
	mux.HandleFunc("POST /api/providers/test", s.handleProviderTest)

	mux.HandleFunc("POST /api/audits", s.handleCreateAudit)
	mux.HandleFunc("GET /api/audits", s.handleListAudits)
	mux.HandleFunc("GET /api/audits/{id}", s.handleGetAudit)
	mux.HandleFunc("GET /api/audits/{id}/graph", s.handleGraph)
	mux.HandleFunc("GET /api/audits/{id}/files", s.handleFiles)
	mux.HandleFunc("GET /api/audits/{id}/file", s.handleFile)
	mux.HandleFunc("GET /api/audits/{id}/ast", s.handleAST)
	mux.HandleFunc("GET /api/audits/{id}/chunks", s.handleChunks)

	mux.HandleFunc("GET /api/audits/{id}/findings", s.handleListFindings)
	mux.HandleFunc("GET /api/audits/{id}/findings/{fid}", s.handleGetFinding)
	mux.HandleFunc("POST /api/audits/{id}/findings/{fid}/state", s.handleSetFindingState)
	mux.HandleFunc("POST /api/audits/{id}/scan", s.handleScan)

	mux.HandleFunc("POST /api/audits/{id}/propose-fix", s.handleProposeFix)
	mux.HandleFunc("POST /api/audits/{id}/apply-patch", s.handleApplyPatch)
	mux.HandleFunc("POST /api/audits/{id}/revert-patch", s.handleRevertPatch)
	mux.HandleFunc("GET /api/audits/{id}/changes", s.handleChanges)
	mux.HandleFunc("GET /api/audits/{id}/changes.patch", s.handleChangesPatch)

	mux.HandleFunc("POST /api/audits/{id}/index", s.handleIndex)
	mux.HandleFunc("GET /api/audits/{id}/index/status", s.handleIndexStatus)

	mux.HandleFunc("POST /api/audits/{id}/advisories", s.handleFetchAdvisories)
	mux.HandleFunc("GET /api/audits/{id}/advisories", s.handleListAdvisories)
	mux.HandleFunc("GET /api/advisories/status", s.handleAdvisoryStatus)

	mux.HandleFunc("POST /api/chat", s.handleChat)

	mux.HandleFunc("GET /api/audits/{id}/export", s.handleExport)

	mux.HandleFunc("GET /ws", s.handleWebSocket)

	return withLogging(withRecover(mux))
}

// ---------------------------------------------------------------- helpers

// apiError is the error envelope defined in docs/API.md.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("server: write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var e apiError
	e.Error.Code = code
	e.Error.Message = msg
	writeJSON(w, status, e)
}

// fail maps an internal error onto an HTTP status and a stable error code.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, findings.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, audit.ErrNoAdvisorySource):
		writeError(w, http.StatusServiceUnavailable, "disabled", "advisory lookups are disabled by configuration")
	case strings.Contains(err.Error(), "escapes the audit workspace"):
		writeError(w, http.StatusBadRequest, "path_escape", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "request body is required")
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return false
	}
	return true
}

// ---------------------------------------------------------------- handlers

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	providers := s.providerInfos(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"version":       version.Short(),
		"workspace":     s.cfg.Workspace,
		"uptimeSeconds": int(time.Since(s.started).Seconds()),
		"providers":     providers,
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	redacted := s.cfg.Redacted()
	// The renderer reads the configuration from a "config" key. The API key
	// is never included: only the redacted configuration is exposed.
	writeJSON(w, http.StatusOK, map[string]any{
		"config": map[string]any{
			"workspace":   redacted.Workspace,
			"providers":   s.providerInfos(r.Context()),
			"ollamaUrl":   strings.Join(redacted.OllamaURLs, ","),
			"chatModel":   redacted.ChatModel,
			"embedModel":  redacted.EmbedModel,
			"scanModel":   redacted.ScanModel,
			"numCtx":      redacted.NumCtx,
			"disableLlm":  redacted.DisableLLM,
			"byokBaseUrl": redacted.BYOKBaseURL,
			"byokModel":   redacted.BYOKModel,
		},
	})
}

func (s *Server) providerInfos(ctx context.Context) []model.ProviderInfo {
	if s.llm == nil {
		return []model.ProviderInfo{}
	}
	info := model.ProviderInfo{ID: s.llm.ID(), Kind: s.llm.Kind()}
	if err := s.llm.Health(ctx); err != nil {
		info.Error = err.Error()
	} else {
		info.OK = true
		if models, err := s.llm.Models(ctx); err == nil {
			info.Models = models
		}
	}
	return []model.ProviderInfo{info}
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.providerInfos(r.Context())})
}

func (s *Server) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		URL   string `json:"url"`
		Model string `json:"model"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "url is required")
		return
	}
	probe := llm.NewOllama(llm.Options{URL: req.URL, Model: req.Model, NumCtx: s.cfg.NumCtx})
	info := model.ProviderInfo{ID: "ollama", Kind: "ollama", URL: req.URL}
	if err := probe.Health(r.Context()); err != nil {
		info.Error = err.Error()
	} else {
		info.OK = true
		if models, err := probe.Models(r.Context()); err == nil {
			info.Models = models
		}
	}
	// The renderer reads the result from a "provider" key, so the envelope
	// is part of the contract.
	writeJSON(w, http.StatusOK, map[string]any{"provider": info})
}

func (s *Server) handleCreateAudit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Package string `json:"package"`
		Version string `json:"version"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Package) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "package is required")
		return
	}

	a, err := s.manager.Create(r.Context(), req.Package, req.Version)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"audit": a})

	// Run the acquisition asynchronously; progress arrives over the WebSocket.
	go func(id string) {
		defer func() {
			// A panic in the background run must not take the whole server
			// down; it is reported as an audit error instead.
			if rec := recover(); rec != nil {
				log.Printf("audit %s panicked: %v", id, rec)
				s.hub.Broadcast(model.Event{Type: model.EventAuditError, AuditID: id, Error: fmt.Sprintf("audit panicked: %v", rec)})
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := s.manager.Run(ctx, id); err != nil {
			s.hub.Broadcast(model.Event{Type: model.EventAuditError, AuditID: id, Error: err.Error()})
		}
	}(a.ID)
}

func (s *Server) handleListAudits(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"audits": s.manager.List()})
}

func (s *Server) handleGetAudit(w http.ResponseWriter, r *http.Request) {
	a, err := s.manager.Snapshot(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": a})
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	g, err := s.manager.Graph(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"graph": g})
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	tree, err := s.manager.FileTree(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tree": tree})
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	f, err := s.manager.File(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": f})
}

func (s *Server) handleAST(w http.ResponseWriter, r *http.Request) {
	a, err := s.manager.AST(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ast": a})
}

func (s *Server) handleChunks(w http.ResponseWriter, r *http.Request) {
	c, err := s.manager.Chunks(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chunks": c})
}

func (s *Server) handleListFindings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := findings.Filter{
		Severities: q["severity"],
		CWEs:       q["cwe"],
		Tools:      q["tool"],
		Files:      q["file"],
		States:     q["state"],
		Packages:   q["package"],
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Offset = n
		}
	}

	list, err := s.store.List(r.Context(), r.PathValue("id"), f)
	if err != nil {
		fail(w, err)
		return
	}
	total, _ := s.store.Total(r.Context(), r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"findings": list, "total": total})
}

func (s *Server) handleGetFinding(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.Get(r.Context(), r.PathValue("id"), r.PathValue("fid"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"finding": f})
}

func (s *Server) handleSetFindingState(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State           string `json:"state"`
		SuppressedUntil string `json:"suppressedUntil"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var until *time.Time
	if req.SuppressedUntil != "" {
		t, err := time.Parse(time.RFC3339, req.SuppressedUntil)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "suppressedUntil must be RFC 3339")
			return
		}
		until = &t
	}
	if err := s.store.SetState(r.Context(), r.PathValue("id"), r.PathValue("fid"), req.State, until); err != nil {
		if errors.Is(err, findings.ErrNotFound) {
			fail(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	f, _ := s.store.Get(r.Context(), r.PathValue("id"), r.PathValue("fid"))
	writeJSON(w, http.StatusOK, map[string]any{"finding": f})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Engine string `json:"engine"`
		Path   string `json:"path"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	engine := req.Engine
	if engine == "" {
		engine = "sast"
	}
	if engine != "sast" && engine != "llm" {
		writeError(w, http.StatusBadRequest, "bad_request", "engine must be \"sast\" or \"llm\"")
		return
	}
	if engine == "llm" && s.cfg.DisableLLM {
		writeError(w, http.StatusServiceUnavailable, "disabled", "LLM features are disabled by configuration")
		return
	}

	id := r.PathValue("id")
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": jobID})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()
		var err error
		if engine == "llm" {
			_, err = s.manager.ScanLLM(ctx, id, req.Path)
		} else {
			_, err = s.manager.Scan(ctx, id)
		}
		if err != nil {
			s.hub.Broadcast(model.Event{Type: model.EventAuditError, AuditID: id, Error: err.Error()})
		}
	}()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if s.index == nil {
		writeError(w, http.StatusServiceUnavailable, "disabled", "the index is not available")
		return
	}
	id := r.PathValue("id")
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": jobID})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if s.llm == nil {
			return
		}
		err := s.index.Embed(ctx, id, s.llm, s.cfg.EmbedModel, func(done, total int) {
			pct := 0.0
			if total > 0 {
				pct = float64(done) / float64(total)
			}
			s.hub.Broadcast(model.Event{
				Type:    model.EventAuditProgress,
				AuditID: id,
				Phase:   "embedding",
				Percent: pct,
				Message: fmt.Sprintf("Embedded %d/%d chunks", done, total),
			})
		})
		if err != nil {
			s.hub.Broadcast(model.Event{Type: model.EventAuditProgress, AuditID: id, Phase: "embedding", Message: "Embedding unavailable: " + err.Error()})
		}
	}()
}

func (s *Server) handleIndexStatus(w http.ResponseWriter, r *http.Request) {
	if s.index == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ready": false, "phase": "unavailable", "progress": 0.0,
			"chunks": 0, "embedded": 0, "keywordOnly": true, "embedModel": "",
		})
		return
	}
	st, err := s.index.Status(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ready":       st.Ready,
		"phase":       st.Phase,
		"progress":    st.Progress,
		"chunks":      st.Chunks,
		"embedded":    st.Embedded,
		"keywordOnly": st.KeywordOnly,
		"embedModel":  st.EmbedModel,
	})
}

func (s *Server) handleProposeFix(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FindingID string `json:"findingId"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if s.llm == nil || s.cfg.DisableLLM {
		writeError(w, http.StatusServiceUnavailable, "disabled", "LLM features are disabled by configuration")
		return
	}
	id := r.PathValue("id")
	f, err := s.store.Get(r.Context(), id, req.FindingID)
	if err != nil {
		fail(w, err)
		return
	}

	// The scanner may already have produced a diff, in which case there is
	// no need to ask the model for one.
	if strings.TrimSpace(f.Patch) != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"patch": model.Patch{
				FindingID: f.ID,
				Unified:   f.Patch,
				Valid:     true,
				Error:     "",
				Summary:   f.Remediation,
			},
		})
		return
	}

	// Otherwise ask the model for a patch against the current content of
	// the file, and check that the diff it produced actually applies.
	root, err := s.manager.AuditRoot(id)
	if err != nil {
		fail(w, err)
		return
	}
	abs, err := audit.ResolvePath(root, f.File)
	if err != nil {
		fail(w, err)
		return
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "the file the finding refers to is not in the workspace")
		return
	}

	fix, err := scan.ProposeFix(r.Context(), s.llm, s.cfg.ChatModel, s.cfg.NumCtx, *f, string(content))
	if err != nil {
		// The model failed. The prose remediation is still useful, so it is
		// returned alongside the error rather than swallowed.
		writeJSON(w, http.StatusOK, map[string]any{
			"patch": model.Patch{
				FindingID: f.ID,
				Unified:   "",
				Valid:     false,
				Error:     err.Error(),
				Summary:   f.Remediation,
			},
		})
		return
	}

	valid := true
	verr := ""
	if err := patch.Validate(string(content), fix.Unified); err != nil {
		valid = false
		verr = err.Error()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"patch": model.Patch{
			FindingID: f.ID,
			Unified:   fix.Unified,
			Valid:     valid,
			Error:     verr,
			Summary:   fix.Explanation,
		},
	})
}

func (s *Server) handleApplyPatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Unified string `json:"unified"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if s.patches == nil {
		writeError(w, http.StatusServiceUnavailable, "disabled", "patch application is not available")
		return
	}
	root, err := s.manager.WorkspaceRoot(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.patches.Apply(r.Context(), root, req.Path, req.Unified); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// Re-scan the patched file and report what changed. The delta is the
	// evidence that the fix worked; without it, applying a patch would only
	// assert that it did.
	rescan, rerr := s.manager.RescanFile(r.Context(), r.PathValue("id"), req.Path)
	if rerr != nil {
		// The patch was applied; only the verification failed. Say so
		// rather than pretending the whole request failed.
		writeJSON(w, http.StatusOK, map[string]any{
			"applied": true,
			"rescan":  nil,
			"error":   rerr.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"applied": true,
		"rescan":  rescan,
	})
}

// handleRevertPatch restores the pre-patch contents of a file.
func (s *Server) handleRevertPatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if s.patches == nil {
		writeError(w, http.StatusServiceUnavailable, "disabled", "patch application is not available")
		return
	}
	root, err := s.manager.WorkspaceRoot(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reverted, err := s.patches.Revert(r.Context(), root, req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !reverted {
		writeError(w, http.StatusNotFound, "not_found", "no applied change to revert for this file")
		return
	}

	// Re-scan so the findings list matches the restored file.
	rescan, rerr := s.manager.RescanFile(r.Context(), r.PathValue("id"), req.Path)
	resp := map[string]any{"reverted": true}
	if rerr == nil {
		resp["rescan"] = rescan
	} else {
		resp["error"] = rerr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleChangesPatch exports the applied changes as a single unified diff
// that can be saved as a .patch file and applied with "git apply".
func (s *Server) handleChangesPatch(w http.ResponseWriter, r *http.Request) {
	if s.patches == nil {
		writeError(w, http.StatusServiceUnavailable, "disabled", "patch application is not available")
		return
	}
	root, err := s.manager.WorkspaceRoot(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	series, err := s.patches.PatchSeries(r.Context(), root)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/x-patch; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("aisai-%s.patch", r.PathValue("id"))))
	_, _ = w.Write([]byte(series))
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	if s.patches == nil {
		writeJSON(w, http.StatusOK, map[string]any{"changes": []model.Change{}})
		return
	}
	root, err := s.manager.WorkspaceRoot(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	changes, err := s.patches.Changes(r.Context(), root)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]model.Change, 0, len(changes))
	for _, c := range changes {
		out = append(out, model.Change{
			Path:      c.Path,
			Unified:   c.Unified,
			AppliedAt: c.AppliedAt,
			Backup:    c.Backup,
			Reverted:  c.Reverted,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": out})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuditID  string        `json:"auditId"`
		Question string        `json:"question"`
		History  []llm.Message `json:"history"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	engine := s.chat
	if s.chatFactory != nil {
		engine = s.chatFactory(req.AuditID)
	}
	if engine == nil {
		writeError(w, http.StatusServiceUnavailable, "disabled", "chat is not available")
		return
	}
	if s.cfg.DisableLLM {
		writeError(w, http.StatusServiceUnavailable, "disabled", "LLM features are disabled by configuration")
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "question is required")
		return
	}

	jobID := fmt.Sprintf("chat-%d", time.Now().UnixNano())
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": jobID})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		res, err := engine.Ask(ctx, ChatRequest{
			AuditID:  req.AuditID,
			Question: req.Question,
			History:  req.History,
		}, func(token string) {
			s.hub.Broadcast(model.Event{Type: model.EventChatToken, JobID: jobID, Data: map[string]any{"delta": token}})
		})
		if err != nil {
			s.hub.Broadcast(model.Event{Type: model.EventChatError, JobID: jobID, Error: err.Error()})
			return
		}
		cits := make([]map[string]any, 0, len(res.Citations))
		for _, c := range res.Citations {
			cits = append(cits, map[string]any{"file": c.File, "line": c.Line, "symbol": c.Symbol})
		}
		s.hub.Broadcast(model.Event{Type: model.EventChatDone, JobID: jobID, Data: map[string]any{
			"citations": cits,
			"answer":    res.Answer,
		}})
	}()
}

// handleFetchAdvisories queries the advisory source for every package in the
// audit and returns the flattened advisory list.
//
// The "offline" field in the body is accepted for API compatibility. The
// effective mode is fixed when the advisory client is constructed, because
// switching modes mid-session would make the results ambiguous.
func (s *Server) handleFetchAdvisories(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Offline bool `json:"offline"`
	}
	if r.ContentLength != 0 {
		if !decodeBody(w, r, &req) {
			return
		}
	}

	advisories, err := s.manager.FetchAdvisories(r.Context(), r.PathValue("id"), true)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"advisories": advisories,
		"mode":       s.manager.AdvisoryMode(),
	})
}

// handleListAdvisories returns the advisories already fetched for an audit.
func (s *Server) handleListAdvisories(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	advisories, err := s.manager.Advisories(id)
	if err != nil {
		fail(w, err)
		return
	}
	if advisories == nil {
		advisories = []model.Advisory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"advisories": advisories,
		"mode":       s.manager.AdvisoryMode(),
	})
}

// handleAdvisoryStatus reports whether the advisory source is the live OSV
// service or a local snapshot.
func (s *Server) handleAdvisoryStatus(w http.ResponseWriter, r *http.Request) {
	st := s.manager.AdvisoryStatus()
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": st.Mode,
		"osv": map[string]any{
			"available": st.Available,
			"updatedAt": st.UpdatedAt,
			"count":     st.Count,
		},
	})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "sarif"
	}
	list, err := s.store.List(r.Context(), id, findings.Filter{})
	if err != nil {
		fail(w, err)
		return
	}

	switch format {
	case "sarif":
		data, err := findings.WriteSARIF(list, findings.SARIFOptions{ToolVersion: version.Short()})
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/sarif+json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("aisai-%s.sarif", id)))
		w.Write(data)
	case "json":
		data, err := findings.WriteJSON(list)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("aisai-%s.json", id)))
		w.Write(data)
	case "cyclonedx", "spdx":
		data, ext, contentType, err := s.writeSBOM(r, id, format, list)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("aisai-%s.%s", id, ext)))
		w.Write(data)
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "format must be sarif, cyclonedx, spdx or json")
	}
}

// writeSBOM builds a software bill of materials for an audit.
func (s *Server) writeSBOM(r *http.Request, id, format string, list []model.Finding) ([]byte, string, string, error) {
	ctx := r.Context()

	packages, err := s.manager.Packages(ctx, id)
	if err != nil {
		return nil, "", "", err
	}
	advisories, err := s.manager.Advisories(id)
	if err != nil {
		return nil, "", "", err
	}

	opts := sbom.Options{
		ToolVersion: version.Short(),
		AuditID:     id,
	}
	if snap, err := s.manager.Snapshot(id); err == nil {
		opts.PackageName = snap.Package
		opts.PackageVersion = snap.Version
	}

	switch format {
	case "cyclonedx":
		data, err := sbom.WriteCycloneDX(packages, advisories, list, opts)
		if err != nil {
			return nil, "", "", err
		}
		return data, "cdx.json", "application/vnd.cyclonedx+json", nil
	default:
		data, err := sbom.WriteSPDX(packages, advisories, list, opts)
		if err != nil {
			return nil, "", "", err
		}
		return data, "spdx.json", "application/spdx+json", nil
	}
}

// ---------------------------------------------------------------- middleware

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// withRecover converts a panic into a 500 rather than killing the process.
func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("server: panic in %s %s: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "internal", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Logf writes to the server log. Used by the audit manager.
func (s *Server) Logf(format string, args ...any) {
	log.Printf(format, args...)
}

// EnsureWorkspace creates the data directory.
func (s *Server) EnsureWorkspace() error {
	if err := s.cfg.EnsureWorkspace(); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(s.cfg.Workspace, "audits"), 0o755)
}

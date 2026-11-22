package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"

	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/terminal"
)

// terminalsDisabled reports that the integrated terminal was switched off with
// AISAI_DISABLE_TERMINAL, for a deployment that must not offer a shell.
func terminalsDisabled() bool { return os.Getenv("AISAI_DISABLE_TERMINAL") != "" }

// terminalManager returns the session manager, creating it on first use.
func (s *Server) terminalManager() *terminal.Manager {
	s.terminalsOnce.Do(func() { s.terminals = terminal.NewManager() })
	return s.terminals
}

// CloseTerminals ends every open shell. The process calls it on shutdown.
func (s *Server) CloseTerminals() {
	if s.terminals != nil {
		s.terminals.CloseAll()
	}
}

func terminalFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, terminal.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such terminal")
	case errors.Is(err, terminal.ErrUnsupported):
		writeError(w, http.StatusNotImplemented, "unsupported", err.Error())
	default:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	}
}

// handleOpenTerminal starts a shell. It opens in the folder of the audit when
// one is given, and in the home folder otherwise. Output arrives as
// terminal.data events; keystrokes go to /api/terminals/{id}/input.
func (s *Server) handleOpenTerminal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuditID string `json:"auditId"`
		Cols    uint16 `json:"cols"`
		Rows    uint16 `json:"rows"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if terminalsDisabled() {
		writeError(w, http.StatusServiceUnavailable, "disabled", "the integrated terminal is disabled by configuration")
		return
	}
	cwd := terminal.HomeDir()
	if req.AuditID != "" && s.manager != nil {
		root, err := s.manager.WorkspaceRoot(req.AuditID)
		if err != nil {
			fail(w, err)
			return
		}
		cwd = root
	}
	id, err := s.terminalManager().Open(terminal.Options{Cwd: cwd, Cols: req.Cols, Rows: req.Rows}, terminal.Handlers{
		Data: func(id string, data []byte) {
			s.hub.Broadcast(model.Event{Type: model.EventTerminalData, JobID: id,
				Data: map[string]any{"data": base64.StdEncoding.EncodeToString(data)}})
		},
		Exit: func(id string, code int) {
			s.hub.Broadcast(model.Event{Type: model.EventTerminalExit, JobID: id, Data: map[string]any{"code": code}})
		},
	})
	if err != nil {
		terminalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "cwd": cwd})
}

func (s *Server) handleTerminalInput(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data string `json:"data"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.terminalManager().Write(r.PathValue("id"), []byte(req.Data)); err != nil {
		terminalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTerminalResize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Cols == 0 || req.Rows == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "cols and rows are required")
		return
	}
	if err := s.terminalManager().Resize(r.PathValue("id"), req.Cols, req.Rows); err != nil {
		terminalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCloseTerminal(w http.ResponseWriter, r *http.Request) {
	if err := s.terminalManager().Close(r.PathValue("id")); err != nil {
		terminalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// jobStatus is the state of a background job, kept so a client that missed
// the job's final event can ask for it.
type jobStatus struct {
	State    string `json:"state"` // running | done | failed
	Error    string `json:"error,omitempty"`
	Findings int    `json:"findings,omitempty"`
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	v, ok := s.jobs.Load(r.PathValue("job"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such job")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": v})
}

// handleCloseAllTerminals ends every shell. The app calls it when it starts,
// so shells left by a reloaded window do not pile up.
func (s *Server) handleCloseAllTerminals(w http.ResponseWriter, _ *http.Request) {
	s.CloseTerminals()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

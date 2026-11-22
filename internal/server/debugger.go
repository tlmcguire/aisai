package server

import (
	"errors"
	"net/http"
	"os"

	"github.com/tlmcguire/aisai/internal/audit"
	"github.com/tlmcguire/aisai/internal/debug"
	"github.com/tlmcguire/aisai/internal/model"
)

// debuggerDisabled reports that running programs was switched off. A
// deployment that must not offer a shell must not run programs either, so
// the terminal's switch turns the debugger off too.
func debuggerDisabled() bool {
	return os.Getenv("AISAI_DISABLE_DEBUGGER") != "" || terminalsDisabled()
}

func (s *Server) debugManager() *debug.Manager {
	s.debuggerOnce.Do(func() { s.debugger = debug.NewManager() })
	return s.debugger
}

// CloseDebugSessions stops every debugged program. The process calls it on
// shutdown.
func (s *Server) CloseDebugSessions() {
	if s.debugger != nil {
		s.debugger.CloseAll()
	}
}

// handleStartDebug runs a Python file of a local project under the debugger.
// Events arrive as debug.event, debug.output and debug.exit.
func (s *Server) handleStartDebug(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuditID     string           `json:"auditId"`
		Program     string           `json:"program"`
		Args        []string         `json:"args"`
		Breakpoints map[string][]int `json:"breakpoints"`
		StopOnEntry bool             `json:"stopOnEntry"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if debuggerDisabled() {
		writeError(w, http.StatusServiceUnavailable, "disabled", "the debugger is disabled by configuration")
		return
	}
	if s.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "disabled", "no audits are available")
		return
	}
	target, err := s.manager.DebugTarget(req.AuditID)
	if err != nil {
		if errors.Is(err, audit.ErrNotLocal) {
			writeError(w, http.StatusForbidden, "not_local", err.Error())
			return
		}
		fail(w, err)
		return
	}
	python := debug.FindPython(target.SourceDir)
	if python == "" {
		writeError(w, http.StatusBadRequest, "no_python", debug.ErrNoPython.Error())
		return
	}
	info, err := s.debugManager().Start(debug.Options{
		Root:        target.Root,
		Dir:         target.ProjectDir,
		Program:     req.Program,
		Args:        req.Args,
		Python:      python,
		Breakpoints: req.Breakpoints,
		StopOnEntry: req.StopOnEntry,
	}, debug.Handlers{
		Event: func(id string, ev map[string]any) {
			s.hub.Broadcast(model.Event{Type: model.EventDebugEvent, JobID: id, AuditID: req.AuditID, Data: ev})
		},
		Output: func(id, stream, text string) {
			s.hub.Broadcast(model.Event{Type: model.EventDebugOutput, JobID: id, AuditID: req.AuditID,
				Data: map[string]any{"stream": stream, "text": text}})
		},
		Exit: func(id string, code int) {
			s.hub.Broadcast(model.Event{Type: model.EventDebugExit, JobID: id, AuditID: req.AuditID, Data: map[string]any{"code": code}})
		},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDebugCommand(w http.ResponseWriter, r *http.Request) {
	var cmd debug.Command
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if !decodeBody(w, r, &cmd) {
		return
	}
	if err := s.debugManager().Send(r.PathValue("id"), cmd); err != nil {
		debugFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleStopDebug(w http.ResponseWriter, r *http.Request) {
	if err := s.debugManager().Stop(r.PathValue("id")); err != nil {
		debugFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func debugFail(w http.ResponseWriter, err error) {
	if errors.Is(err, debug.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "the program is no longer running")
		return
	}
	writeError(w, http.StatusBadRequest, "bad_request", err.Error())
}

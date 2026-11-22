package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/assist"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
)

// FileWriter is implemented by a PatchApplier that can save a whole file.
type FileWriter interface {
	Write(ctx context.Context, root, path, content string) (bool, error)
}

// maxAssistBody bounds the body of an editor request.
const maxAssistBody = 8 << 20

// handleSaveFile saves an edit made in the editor.
//
// The save goes through the same path as a patch: the file is backed up, the
// change is recorded as a diff, and the file is re-scanned, so the Changes
// view, the exported patch series and Revert all know about it and the
// findings follow the new text.
func (s *Server) handleSaveFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAssistBody)
	if !decodeBody(w, r, &req) {
		return
	}
	writer, ok := s.patches.(FileWriter)
	if s.patches == nil || !ok {
		writeError(w, http.StatusServiceUnavailable, "disabled", "saving files is not available")
		return
	}
	id := r.PathValue("id")
	root, err := s.manager.WorkspaceRoot(id)
	if err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "path is required")
		return
	}
	changed, err := writer.Write(r.Context(), root, req.Path, req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resp := map[string]any{"saved": true, "changed": changed}
	if changed {
		rescan, rerr := s.manager.RescanFile(r.Context(), id, req.Path)
		if rerr != nil {
			// The file is saved; only the re-scan failed.
			resp["error"] = rerr.Error()
		} else {
			resp["rescan"] = rescan
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// modelReady reports whether an editor request can use the model, and writes
// the error response when it cannot.
func (s *Server) modelReady(w http.ResponseWriter) bool {
	if s.llm == nil || s.cfg.DisableLLM {
		writeError(w, http.StatusServiceUnavailable, "disabled", "no model is available: choose one in Settings")
		return false
	}
	return true
}

// handleComplete returns inline code completion for the cursor position.
func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path"`
		Language string `json:"language"`
		Prefix   string `json:"prefix"`
		Suffix   string `json:"suffix"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAssistBody)
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.modelReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	text, err := assist.Complete(ctx, s.llm, s.models.Chat(), s.models.NumCtx(), assist.CompleteRequest{
		Path: req.Path, Language: req.Language, Prefix: req.Prefix, Suffix: req.Suffix,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "model_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"completion": text, "model": s.models.Chat()})
}

// handleEditSelection asks the model to rewrite a selection.
//
// It returns the replacement only: nothing is written. The editor shows it
// as a diff and applies it to its own buffer when the user accepts.
func (s *Server) handleEditSelection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path        string `json:"path"`
		Language    string `json:"language"`
		Before      string `json:"before"`
		Selection   string `json:"selection"`
		After       string `json:"after"`
		Instruction string `json:"instruction"`
		StartLine   int    `json:"startLine"`
		EndLine     int    `json:"endLine"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAssistBody)
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.modelReady(w) {
		return
	}
	var notes []string
	if req.StartLine > 0 && s.store != nil {
		if list, err := s.store.List(r.Context(), r.PathValue("id"), findingsFilterFor(req.Path)); err == nil {
			for _, f := range list {
				if f.EndLine >= req.StartLine && f.StartLine <= max(req.EndLine, req.StartLine) {
					notes = append(notes, fmt.Sprintf("%s (%s, line %d): %s", f.CWE, f.Severity, f.StartLine, f.Message))
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	replacement, err := assist.Edit(ctx, s.llm, s.models.Chat(), s.models.NumCtx(), assist.EditRequest{
		Path: req.Path, Language: req.Language, Before: req.Before, Selection: req.Selection,
		After: req.After, Instruction: req.Instruction, Findings: notes,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "model_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"replacement": replacement, "model": s.models.Chat()})
}

func findingsFilterFor(path string) findings.Filter {
	return findings.Filter{Files: []string{model.RelPath(path)}}
}

// handleChatTitle names a conversation from its first exchange.
func (s *Server) handleChatTitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var provider = s.llm
	if s.cfg.DisableLLM {
		provider = nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	numCtx, chatModel := 0, ""
	if s.models != nil {
		numCtx, chatModel = s.models.NumCtx(), s.models.Chat()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"title": assist.Title(ctx, provider, chatModel, numCtx, req.Question, req.Answer),
	})
}

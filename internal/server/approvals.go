package server

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// approvalTimeout is how long a risky tool call waits for the user. No answer
// is a refusal: nothing risky runs because nobody was looking.
const approvalTimeout = 2 * time.Minute

// approvalBook holds the tool calls that wait for the user's answer.
type approvalBook struct {
	mu      sync.Mutex
	waiting map[string]chan bool
}

// wait blocks until the user answers the request, the timeout passes or the
// context ends. Anything but an explicit yes is a no. timedOut tells a
// refusal from nobody answering, so the app can say which happened.
func (b *approvalBook) wait(ctx context.Context, key string) (ok, timedOut bool, err error) {
	ch := make(chan bool, 1)
	b.mu.Lock()
	if b.waiting == nil {
		b.waiting = map[string]chan bool{}
	}
	b.waiting[key] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.waiting, key)
		b.mu.Unlock()
	}()

	timer := time.NewTimer(approvalTimeout)
	defer timer.Stop()
	select {
	case answer := <-ch:
		return answer, false, nil
	case <-timer.C:
		return false, true, nil
	case <-ctx.Done():
		return false, false, ctx.Err()
	}
}

// answer delivers the user's decision. It reports whether anything was waiting.
func (b *approvalBook) answer(key string, ok bool) bool {
	b.mu.Lock()
	ch := b.waiting[key]
	b.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- ok:
	default:
	}
	return true
}

// handleChatApproval records the user's decision on a tool call. The id is
// "<jobId>/<stepId>".
func (s *Server) handleChatApproval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JobID    string `json:"jobId"`
		Approved bool   `json:"approved"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.approvals.answer(req.JobID+"/"+r.PathValue("id"), req.Approved) {
		writeError(w, http.StatusNotFound, "not_found", "nothing is waiting for that approval (it may have timed out)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approved": req.Approved})
}

// handleSetWebSearch switches the chat's web access on or off and saves the
// choice. It takes effect from the next chat turn.
func (s *Server) handleSetWebSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	s.cfg.WebSearch = req.Enabled
	if err := s.cfg.Save(); err != nil {
		// The choice still holds for this session; say that it was not kept.
		writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled, "saved": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled, "saved": true})
}

// handleChatCapabilities says what the chat model can do, so the app can
// warn before a question instead of after it: "tools" is "yes", "no" or
// "unknown" (an OpenAI-compatible server has no standard way to tell).
// Without tools the model cannot search the web, read files or run commands.
func (s *Server) handleChatCapabilities(w http.ResponseWriter, r *http.Request) {
	model := ""
	if s.models != nil {
		model = s.models.Chat()
	}
	out := map[string]any{"model": model, "tools": "unknown"}
	type toolProber interface {
		SupportsTools(ctx context.Context, model string) (supported, known bool, err error)
	}
	if p, ok := s.llm.(toolProber); ok && model != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		yes, known, err := p.SupportsTools(ctx, model)
		switch {
		case err != nil:
			out["error"] = err.Error()
		case !known:
		case yes:
			out["tools"] = "yes"
		default:
			out["tools"] = "no"
		}
	}
	writeJSON(w, http.StatusOK, out)
}

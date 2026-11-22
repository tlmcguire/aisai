package server

import (
	"context"

	"github.com/tlmcguire/aisai/internal/chat"
	"github.com/tlmcguire/aisai/internal/llm"
)

// chatAdapter bridges the chat package to the server's ChatEngine interface.
//
// The two packages deliberately do not share a result type: the server's
// interface is the stable API contract, while the chat package owns the
// richer result (quarantined chunks, keyword-only state) that the UI wants.
type chatAdapter struct {
	chat *chat.Chat
}

// NewChatEngine wraps a chat engine so the server can drive it.
func NewChatEngine(c *chat.Chat) ChatEngine {
	return &chatAdapter{chat: c}
}

// Ask answers a question about the audited package.
func (a *chatAdapter) Ask(ctx context.Context, req ChatRequest, emit func(token string)) (*ChatResult, error) {
	creq := chat.Request{
		AuditID:  req.AuditID,
		Question: req.Question,
		History:  req.History,
	}
	if sel := req.Selection; sel != nil {
		creq.Selection = &chat.Selection{File: sel.File, StartLine: sel.StartLine, EndLine: sel.EndLine, Text: sel.Text}
	}
	if req.OnStep != nil {
		creq.OnStep = func(st chat.Step) {
			req.OnStep(ChatStep{Kind: st.Kind, Think: st.Think, ID: st.ID, Name: st.Name, Args: st.Args, Output: st.Output, Error: st.Error, Summary: st.Summary, Approved: st.Approved})
		}
	}
	if req.Approve != nil {
		creq.Approve = func(ctx context.Context, ar chat.ApprovalRequest) (bool, error) {
			return req.Approve(ctx, ar.ID)
		}
	}
	res, err := a.chat.Ask(ctx, creq, emit)
	if err != nil {
		return nil, err
	}

	out := &ChatResult{
		Answer:     res.Answer,
		ChunksUsed: res.ChunksUsed,
	}
	for _, c := range res.Citations {
		out.Citations = append(out.Citations, ChatCitation{
			File:   c.File,
			Line:   c.Line,
			Symbol: c.Symbol,
		})
	}
	return out, nil
}

// Ensure the adapter satisfies the interface.
var _ ChatEngine = (*chatAdapter)(nil)

// Ensure the llm package is referenced so the import stays meaningful when
// the history type changes.
var _ = []llm.Message(nil)

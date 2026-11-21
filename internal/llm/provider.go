// Package llm provides a provider-agnostic client for large language models.
//
// Two implementations ship here: OllamaProvider (local, default) and
// OpenAIProvider (any OpenAI-compatible endpoint, optional BYOK). Both speak
// the same Provider interface so the rest of the backend never has to know
// which backend is answering.
//
// Two rules are enforced by every implementation in this package:
//
//   - num_ctx is always sent explicitly. The legacy application never set it
//     and silently truncated input at Ollama's 2048-token default.
//   - an API key never appears in an error string or a log line.
package llm

import (
	"context"
	"strings"
	"time"
)

// Role identifies the author of a chat message.
type Role string

// Conversation roles.
const (
	// RoleSystem carries instructions that frame the whole conversation.
	RoleSystem Role = "system"
	// RoleUser carries input from the person asking.
	RoleUser Role = "user"
	// RoleAssistant carries model output.
	RoleAssistant Role = "assistant"
)

// DefaultNumCtx is the context window requested from a provider when the
// caller does not ask for a specific one. It is deliberately larger than
// Ollama's 2048-token default, which silently truncated long files.
const DefaultNumCtx = 8192

// DefaultSeed is the fixed seed used when the caller supplies none, so that
// identical requests produce identical answers.
const DefaultSeed = 42

// DefaultTimeout is the per-request timeout used when Options.Timeout is unset.
const DefaultTimeout = 120 * time.Second

// Message is one turn of a chat conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Tool describes a function the model is allowed to call.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ChatRequest is a request for a streamed chat completion.
//
// Schema, when non-nil, is a JSON Schema that constrains the response shape.
// NumCtx, Temperature and Seed are per-request overrides of the provider
// defaults; zero values fall back to the provider configuration.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	Schema      map[string]any
	NumCtx      int
	Temperature float64
	Seed        int
	Think       bool
}

// StreamChunk is one increment of a streamed response.
//
// Delta carries new text. Done is set on the terminal chunk that is emitted
// after a stream error; a stream that ends normally simply closes its
// channel.
type StreamChunk struct {
	Delta string
	Done  bool
}

// CompletionRequest is a request for a single, non-streamed completion.
// Schema, when non-nil, constrains the response to a JSON object.
type CompletionRequest struct {
	Model       string
	System      string
	User        string
	Schema      map[string]any
	NumCtx      int
	Temperature float64
	Seed        int
	Think       bool
}

// Completion is a non-streamed response. JSON holds the parsed object when
// the provider returned valid JSON, and is nil otherwise.
type Completion struct {
	Text string
	JSON map[string]any
}

// Provider is a large language model backend.
type Provider interface {
	// ID returns the stable identifier of this provider instance.
	ID() string
	// Kind returns the provider family: "ollama" or "openai".
	Kind() string
	// Chat streams a completion. The returned channel is closed when the
	// stream ends. Errors that happen before the first byte are returned
	// directly; errors mid-stream are logged and signalled by a terminal
	// chunk with Done set.
	Chat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
	// Complete performs a single completion and parses a structured reply.
	Complete(ctx context.Context, req CompletionRequest) (*Completion, error)
	// Embed returns one embedding vector per input text.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Models lists the models available on the backend.
	Models(ctx context.Context) ([]string, error)
	// Health probes the backend.
	Health(ctx context.Context) error
}

// ToolCall describes a tool invocation requested by the model.
type ToolCall struct {
	// ID is the backend-assigned call identifier, when the backend has one.
	ID string
	// Name is the name of the tool to run.
	Name string
	// Arguments are the decoded call arguments.
	Arguments map[string]any
}

// ToolEvent is one event of a tool-aware stream. Exactly one of Delta and
// ToolCall is meaningful at a time.
type ToolEvent struct {
	Delta    string
	ToolCall *ToolCall
	Done     bool
}

// ToolProvider is the optional interface implemented by providers that can
// surface tool calls. Callers must type-assert for it and fall back to
// retrieval-only behaviour when the assertion fails or the provider rejects
// the tools.
type ToolProvider interface {
	// ChatTools streams a completion and reports tool calls as they arrive.
	ChatTools(ctx context.Context, req ChatRequest) (<-chan ToolEvent, error)
}

// Options configures a provider client.
type Options struct {
	// URL is the provider base URL. For Ollama it may be a comma-separated
	// list of candidate URLs; they are tried in order until one answers.
	URL string
	// Model is the default chat model.
	Model string
	// EmbedModel is the default embedding model.
	EmbedModel string
	// APIKey is the bearer token for OpenAI-compatible endpoints.
	APIKey string
	// Timeout bounds a single HTTP request.
	Timeout time.Duration
	// NumCtx is the context window sent to the backend.
	NumCtx int
	// KeepAlive is how long the backend keeps the model resident after the
	// last request. An empty value means DefaultKeepAlive.
	KeepAlive string
	// Models holds the models in use. When it is set, the provider reads
	// the chat, scan and embedding model names from it on every request, so
	// the desktop app can switch models without a restart. When it is nil,
	// the provider uses Model, EmbedModel and NumCtx.
	Models *Models
}

// numCtx returns the effective context window, defaulting to DefaultNumCtx.
func (o Options) numCtx() int {
	if o.NumCtx > 0 {
		return o.NumCtx
	}
	return DefaultNumCtx
}

// timeout returns the effective request timeout.
func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultTimeout
}

// chatModel returns the effective chat model.
func (o Options) chatModel() string { return o.Model }

// embedModel returns the effective embedding model, falling back to the chat
// model when no dedicated embedding model is configured.
func (o Options) embedModel() string {
	if o.EmbedModel != "" {
		return o.EmbedModel
	}
	return o.Model
}

// splitURLs splits a comma-separated URL list, trimming blanks and dropping
// empty entries.
func splitURLs(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, strings.TrimRight(part, "/"))
		}
	}
	return out
}

// redact removes a secret from a string so it can safely be logged or
// returned to the user.
func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// backoffDelay returns the delay before the given retry attempt (1-based).
func backoffDelay(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 250 * time.Millisecond
	case attempt == 2:
		return 500 * time.Millisecond
	default:
		return time.Second
	}
}

// sleepCtx waits for d, or returns false if the context is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

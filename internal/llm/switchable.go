package llm

import (
	"context"
	"errors"
	"sync"

	"github.com/tlmcguire/aisai/internal/model"
)

// The provider families the desktop app can configure. Every family other
// than Ollama speaks the OpenAI-compatible API.
const (
	KindOllama   = "ollama"
	KindVLLM     = "vllm"
	KindLlamaCPP = "llamacpp"
	KindLMStudio = "lmstudio"
	KindOpenAI   = "openai"

	// Hosted providers reached through their OpenAI-compatible endpoints.
	KindAnthropic  = "anthropic"
	KindGemini     = "gemini"
	KindOpenRouter = "openrouter"
	KindGroq       = "groq"
	KindMistral    = "mistral"
)

// IsProviderKind reports whether kind names a provider family.
func IsProviderKind(kind string) bool {
	switch kind {
	case KindOllama, KindVLLM, KindLlamaCPP, KindLMStudio, KindOpenAI,
		KindAnthropic, KindGemini, KindOpenRouter, KindGroq, KindMistral:
		return true
	}
	return false
}

// IsHostedKind reports whether a kind is a cloud service that needs an API key.
func IsHostedKind(kind string) bool {
	switch kind {
	case KindOpenAI, KindAnthropic, KindGemini, KindOpenRouter, KindGroq, KindMistral:
		return true
	}
	return false
}

// HostedHeaders returns the extra headers a hosted kind needs.
func HostedHeaders(kind, apiKey string) map[string]string {
	if kind == KindAnthropic && apiKey != "" {
		return map[string]string{"x-api-key": apiKey, "anthropic-version": "2023-06-01"}
	}
	return nil
}

// ErrNotSupported is returned by an optional operation the active provider
// does not implement, for example unloading models from a vLLM server.
var ErrNotSupported = errors.New("llm: the active provider does not support this operation")

// Switchable is a Provider whose backend can be replaced while the server
// runs. Every consumer holds the Switchable, so a provider chosen in the
// desktop app takes effect for the next scan and the next chat without a
// restart.
type Switchable struct {
	mu    sync.RWMutex
	inner Provider
	kind  string
	url   string
}

// NewSwitchable wraps a provider. kind is the family reported to the app.
func NewSwitchable(p Provider, kind, url string) *Switchable {
	return &Switchable{inner: p, kind: kind, url: url}
}

// Set replaces the active provider.
func (s *Switchable) Set(p Provider, kind, url string) {
	s.mu.Lock()
	s.inner, s.kind, s.url = p, kind, url
	s.mu.Unlock()
}

func (s *Switchable) current() Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inner
}

// URL returns the base URL of the active provider.
func (s *Switchable) URL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.url
}

// ID returns the family of the active provider, which is also its id.
func (s *Switchable) ID() string { return s.Kind() }

// Kind returns the family of the active provider.
func (s *Switchable) Kind() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kind
}

// Chat implements Provider.
func (s *Switchable) Chat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	return s.current().Chat(ctx, req)
}

// Complete implements Provider.
func (s *Switchable) Complete(ctx context.Context, req CompletionRequest) (*Completion, error) {
	return s.current().Complete(ctx, req)
}

// Embed implements Provider.
func (s *Switchable) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return s.current().Embed(ctx, texts)
}

// Models implements Provider.
func (s *Switchable) Models(ctx context.Context) ([]string, error) {
	return s.current().Models(ctx)
}

// Health implements Provider.
func (s *Switchable) Health(ctx context.Context) error {
	return s.current().Health(ctx)
}

// ListModels describes the installed models. Ollama reports sizes; an
// OpenAI-compatible server only reports names.
func (s *Switchable) ListModels(ctx context.Context) ([]model.ModelInfo, error) {
	p := s.current()
	if lister, ok := p.(interface {
		ListModels(context.Context) ([]model.ModelInfo, error)
	}); ok {
		return lister.ListModels(ctx)
	}
	names, err := p.Models(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.ModelInfo, 0, len(names))
	for _, n := range names {
		out = append(out, model.ModelInfo{Name: n})
	}
	return out, nil
}

// Unload releases models from memory when the active provider can.
func (s *Switchable) Unload(ctx context.Context, names []string) error {
	if u, ok := s.current().(interface {
		Unload(context.Context, []string) error
	}); ok {
		return u.Unload(ctx, names)
	}
	return ErrNotSupported
}

// Delegate exposes the active provider for type checks in tests.
func (s *Switchable) Delegate() Provider { return s.current() }

// ChatTools forwards a tool-aware stream when the active provider supports
// it. Callers fall back to retrieval-only chat on ErrNotSupported.
func (s *Switchable) ChatTools(ctx context.Context, req ChatRequest) (<-chan ToolEvent, error) {
	if tp, ok := s.current().(ToolProvider); ok {
		return tp.ChatTools(ctx, req)
	}
	return nil, ErrNotSupported
}

var (
	_ Provider     = (*Switchable)(nil)
	_ ToolProvider = (*Switchable)(nil)
)

// SupportsThink reports whether the active provider's model shows its
// reasoning. A provider that cannot tell answers false.
func (s *Switchable) SupportsThink(ctx context.Context, model string) (bool, error) {
	if tp, ok := s.current().(interface {
		SupportsThink(context.Context, string) (bool, error)
	}); ok {
		return tp.SupportsThink(ctx, model)
	}
	return false, nil
}

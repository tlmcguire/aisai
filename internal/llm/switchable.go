package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// ErrToolsUnsupported is returned when the model refuses a request that
// offers it tools. It wraps ErrNotSupported, so callers that fall back to a
// plain answer keep working, and lets them tell the user why.
var ErrToolsUnsupported = fmt.Errorf("the model cannot call tools: %w", ErrNotSupported)

// toolsError turns a failed tool request into ErrToolsUnsupported when the
// server says the model has no tool support, and into a plain error
// otherwise. Servers word it differently: Ollama says "does not support
// tools", llama.cpp and LM Studio mention tools or functions together with
// "not supported".
func toolsError(provider string, code int, status, body string) error {
	msg := strings.TrimSpace(body)
	lower := strings.ToLower(msg)
	if code >= 400 && code < 500 && (strings.Contains(lower, "tool") || strings.Contains(lower, "function")) &&
		(strings.Contains(lower, "not support") || strings.Contains(lower, "unsupported") || strings.Contains(lower, "does not support")) {
		return fmt.Errorf("%s: %s: %w", provider, msg, ErrToolsUnsupported)
	}
	return fmt.Errorf("%s: chat: %s: %s", provider, status, msg)
}

// Switchable is a Provider whose backend can be replaced while the server
// runs. Every consumer holds the Switchable, so a provider chosen in the
// desktop app takes effect for the next scan and the next chat without a
// restart.
type Switchable struct {
	mu    sync.RWMutex
	inner Provider
	kind  string
	url   string
	// noTools remembers the models a server refused tool calls for, as
	// "url|model". An OpenAI-compatible server has no way to ask in advance,
	// so the first refusal is kept and the app can warn before the next
	// question. It is forgotten when the provider is switched.
	noTools map[string]bool
}

func toolsKey(url, model string) string { return url + "|" + model }

// NewSwitchable wraps a provider. kind is the family reported to the app.
func NewSwitchable(p Provider, kind, url string) *Switchable {
	return &Switchable{inner: p, kind: kind, url: url}
}

// Set replaces the active provider.
func (s *Switchable) Set(p Provider, kind, url string) {
	s.mu.Lock()
	s.inner, s.kind, s.url = p, kind, url
	s.noTools = nil
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
		events, err := tp.ChatTools(ctx, req)
		if errors.Is(err, ErrToolsUnsupported) {
			s.mu.Lock()
			if s.noTools == nil {
				s.noTools = map[string]bool{}
			}
			s.noTools[toolsKey(s.url, req.Model)] = true
			s.mu.Unlock()
		}
		return events, err
	}
	return nil, ErrNotSupported
}

var (
	_ Provider     = (*Switchable)(nil)
	_ ToolProvider = (*Switchable)(nil)
)

// SupportsTools reports whether the active provider's model can call tools.
// known is false when the provider cannot tell (an OpenAI-compatible server
// has no standard way to ask), in which case a chat simply tries.
func (s *Switchable) SupportsTools(ctx context.Context, model string) (supported, known bool, err error) {
	if tp, ok := s.current().(interface {
		SupportsTools(context.Context, string) (bool, error)
	}); ok {
		yes, err := tp.SupportsTools(ctx, model)
		return yes, err == nil, err
	}
	if _, ok := s.current().(ToolProvider); !ok {
		return false, true, nil
	}
	// The server could not be asked, but it may already have refused.
	s.mu.RLock()
	refused := s.noTools[toolsKey(s.url, model)]
	s.mu.RUnlock()
	if refused {
		return false, true, nil
	}
	return true, false, nil
}

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

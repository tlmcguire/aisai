package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestToolsErrorRecognisesUnsupportedModels(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"error":"registry.ollama.ai/library/gemma:2b does not support tools"}`, true},
		{`{"error":{"message":"tools param is not supported for this model"}}`, true},
		{`function calling is unsupported`, true},
		{`{"error":"model not found"}`, false},
	}
	for _, c := range cases {
		err := toolsError("ollama", 400, "400 Bad Request", c.body)
		if got := errors.Is(err, ErrToolsUnsupported); got != c.want {
			t.Errorf("%s: unsupported = %v, want %v", c.body, got, c.want)
		}
		if c.want && !errors.Is(err, ErrNotSupported) {
			t.Errorf("%s: must still read as ErrNotSupported for the fallback", c.body)
		}
	}
	if err := toolsError("ollama", 500, "500", "tools not supported"); errors.Is(err, ErrToolsUnsupported) {
		t.Error("a server error is not a missing capability")
	}
}

func TestOllamaSupportsTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"capabilities":["completion","tools"]}`))
	}))
	defer srv.Close()
	p := newTestOllama(t, srv)
	yes, err := p.SupportsTools(context.Background(), "qwen3")
	if err != nil || !yes {
		t.Fatalf("SupportsTools = %v, %v", yes, err)
	}
	think, err := p.SupportsThink(context.Background(), "qwen3")
	if err != nil || think {
		t.Fatalf("SupportsThink = %v, %v", think, err)
	}
}

// A server that cannot be asked about tools is believed once it has refused.
func TestSwitchableRemembersAToolsRefusal(t *testing.T) {
	refuse := &toolsRefuser{}
	s := NewSwitchable(refuse, "openai", "http://x")
	if _, known, _ := s.SupportsTools(context.Background(), "m"); known {
		t.Fatal("nothing is known before a question")
	}
	if _, err := s.ChatTools(context.Background(), ChatRequest{Model: "m"}); !errors.Is(err, ErrToolsUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if yes, known, _ := s.SupportsTools(context.Background(), "m"); yes || !known {
		t.Fatalf("after a refusal: supported=%v known=%v", yes, known)
	}
	if _, known, _ := s.SupportsTools(context.Background(), "other"); known {
		t.Fatal("another model is still unknown")
	}
	s.Set(refuse, "openai", "http://y")
	if _, known, _ := s.SupportsTools(context.Background(), "m"); known {
		t.Fatal("switching the provider forgets the refusal")
	}
}

// toolsRefuser is a provider whose tool requests are always refused.
type toolsRefuser struct{ Provider }

func (toolsRefuser) ChatTools(context.Context, ChatRequest) (<-chan ToolEvent, error) {
	return nil, ErrToolsUnsupported
}

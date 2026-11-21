package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestOpenAI returns a provider pointed at the given test server.
func newTestOpenAI(t *testing.T, srv *httptest.Server, key string) *OpenAIProvider {
	t.Helper()
	return NewOpenAICompat(Options{URL: srv.URL, Model: "gpt-test", EmbedModel: "embed-test", APIKey: key, Timeout: 5 * time.Second})
}

func TestOpenAIBaseAddsVersionPath(t *testing.T) {
	if got := openAIBase("https://example.com"); got != "https://example.com/v1" {
		t.Fatalf("base = %q", got)
	}
	if got := openAIBase("https://example.com/v1/"); got != "https://example.com/v1" {
		t.Fatalf("base = %q", got)
	}
}

func TestOpenAIChatStreamsSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Errorf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi \"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"there\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newTestOpenAI(t, srv, "secret-key")
	ch, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var got strings.Builder
	for chunk := range ch {
		got.WriteString(chunk.Delta)
	}
	if got.String() != "Hi there" {
		t.Fatalf("stream = %q", got.String())
	}
}

func TestOpenAICompleteParsesStructuredReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if _, ok := body["response_format"]; !ok {
			t.Errorf("expected response_format in %v", body)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`)
	}))
	defer srv.Close()

	p := newTestOpenAI(t, srv, "secret-key")
	comp, err := p.Complete(context.Background(), CompletionRequest{
		User:   "hi",
		Schema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if comp.JSON == nil || comp.JSON["ok"] != true {
		t.Fatalf("json = %v", comp.JSON)
	}
}

func TestOpenAIEmbedReturnsVectorsInOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		io.WriteString(w, `{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`)
	}))
	defer srv.Close()

	p := newTestOpenAI(t, srv, "secret-key")
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][1] != 1 {
		t.Fatalf("vectors = %v", vecs)
	}
}

func TestOpenAINeverLeaksAPIKey(t *testing.T) {
	const key = "super-secret-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid key super-secret-key"}}`)
	}))
	defer srv.Close()

	p := newTestOpenAI(t, srv, key)
	_, err := p.Complete(context.Background(), CompletionRequest{User: "hi"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaks the API key: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Fatalf("expected the key to be redacted: %v", err)
	}
}

func TestOpenAIHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		io.WriteString(w, `{"data":[{"id":"gpt-test"}]}`)
	}))
	defer srv.Close()

	p := newTestOpenAI(t, srv, "secret-key")
	if err := p.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0] != "gpt-test" {
		t.Fatalf("models = %v", models)
	}
}

func TestOpenAIToolCallsAreAccumulated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"get_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"a.py\\\"}\"}}]}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newTestOpenAI(t, srv, "secret-key")
	events, err := p.ChatTools(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		Tools:    []Tool{{Name: "get_file"}},
	})
	if err != nil {
		t.Fatalf("ChatTools: %v", err)
	}
	var calls []ToolCall
	for ev := range events {
		if ev.ToolCall != nil {
			calls = append(calls, *ev.ToolCall)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %v", calls)
	}
	if calls[0].Name != "get_file" || calls[0].Arguments["path"] != "a.py" {
		t.Fatalf("call = %+v", calls[0])
	}
}

package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Provider implementations must satisfy the interface.
var (
	_ Provider     = (*OllamaProvider)(nil)
	_ ToolProvider = (*OllamaProvider)(nil)
	_ Provider     = (*OpenAIProvider)(nil)
	_ ToolProvider = (*OpenAIProvider)(nil)
)

// newTestOllama returns a provider pointed at the given test server.
func newTestOllama(t *testing.T, srv *httptest.Server) *OllamaProvider {
	t.Helper()
	return NewOllama(Options{URL: srv.URL, Model: "test-model", EmbedModel: "test-embed", Timeout: 5 * time.Second})
}

func TestOllamaChatStreamsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, "{\"message\":{\"content\":\"Hello \"},\"done\":false}\n")
		io.WriteString(w, "{\"message\":{\"content\":\"world\"},\"done\":false}\n")
		io.WriteString(w, "{\"message\":{\"content\":\"\"},\"done\":true}\n")
	}))
	defer srv.Close()

	p := newTestOllama(t, srv)
	ch, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var got strings.Builder
	for chunk := range ch {
		if chunk.Done {
			t.Fatal("unexpected Done chunk")
		}
		got.WriteString(chunk.Delta)
	}
	if got.String() != "Hello world" {
		t.Fatalf("stream = %q, want %q", got.String(), "Hello world")
	}
}

func TestOllamaSendsExplicitNumCtxAndSeed(t *testing.T) {
	var mu sync.Mutex
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(raw, &body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, "{\"message\":{\"content\":\"ok\"},\"done\":false}\n")
		io.WriteString(w, "{\"message\":{\"content\":\"\"},\"done\":true}\n")
	}))
	defer srv.Close()

	p := newTestOllama(t, srv)
	ch, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for range ch {
	}

	mu.Lock()
	defer mu.Unlock()
	opts, ok := body["options"].(map[string]any)
	if !ok {
		t.Fatalf("no options in request: %v", body)
	}
	if got := opts["num_ctx"]; got != float64(DefaultNumCtx) {
		t.Fatalf("num_ctx = %v, want %d", got, DefaultNumCtx)
	}
	if got := opts["temperature"]; got != float64(0) {
		t.Fatalf("temperature = %v, want 0", got)
	}
	if got := opts["seed"]; got != float64(DefaultSeed) {
		t.Fatalf("seed = %v, want %d", got, DefaultSeed)
	}
}

func TestOllamaCompleteParsesStructuredReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"message":{"content":"{\"queries\":[\"a\",\"b\"]}"},"done":true}`)
	}))
	defer srv.Close()

	p := newTestOllama(t, srv)
	comp, err := p.Complete(context.Background(), CompletionRequest{User: "hello"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if comp.JSON == nil {
		t.Fatal("expected parsed JSON")
	}
	q, ok := comp.JSON["queries"].([]any)
	if !ok || len(q) != 2 {
		t.Fatalf("queries = %v", comp.JSON["queries"])
	}
}

func TestOllamaEmbedBatchesInput(t *testing.T) {
	var mu sync.Mutex
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(raw, &payload)
		mu.Unlock()
		io.WriteString(w, `{"embeddings":[[1,0],[0,1]]}`)
	}))
	defer srv.Close()

	p := newTestOllama(t, srv)
	vecs, err := p.Embed(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 2 {
		t.Fatalf("vectors = %v", vecs)
	}
	mu.Lock()
	defer mu.Unlock()
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("input = %v, want an array of 2", payload["input"])
	}
}

func TestOllamaRetriesOnServerError(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "boom")
			return
		}
		io.WriteString(w, `{"models":[{"name":"a"},{"name":"b"}]}`)
	}))
	defer srv.Close()

	p := newTestOllama(t, srv)
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %v", models)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestOllamaFailsOverToNextHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"models":[{"name":"a"}]}`)
	}))
	defer srv.Close()

	// The first candidate is not listening; the second one answers.
	p := NewOllama(Options{URL: "http://127.0.0.1:1," + srv.URL, Model: "m", Timeout: 5 * time.Second})
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0] != "a" {
		t.Fatalf("models = %v", models)
	}
}

func TestOllamaHealthAndModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		io.WriteString(w, `{"models":[{"name":"gemma3n"}]}`)
	}))
	defer srv.Close()

	p := newTestOllama(t, srv)
	if err := p.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0] != "gemma3n" {
		t.Fatalf("models = %v", models)
	}
}

func TestOllamaToolCallsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, `{"message":{"content":"","tool_calls":[{"function":{"name":"get_file","arguments":{"path":"a.py"}}}]},"done":false}`+"\n")
		io.WriteString(w, `{"message":{"content":""},"done":true}`+"\n")
	}))
	defer srv.Close()

	p := newTestOllama(t, srv)
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

func TestOllamaChatReturnsErrorOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"bad model"}`)
	}))
	defer srv.Close()

	p := newTestOllama(t, srv)
	if _, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err == nil {
		t.Fatal("expected an error")
	}
}

package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestOllamaSendsKeepAlive checks that every chat request carries the
// keep-alive window, so Ollama releases the model once the work is done.
func TestOllamaSendsKeepAlive(t *testing.T) {
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

	p := NewOllama(Options{URL: srv.URL, Model: "m", Timeout: 5 * time.Second})
	ch, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for range ch {
	}

	mu.Lock()
	got := body["keep_alive"]
	mu.Unlock()
	if got != DefaultKeepAlive {
		t.Errorf("keep_alive = %v, want %q", got, DefaultKeepAlive)
	}
}

// TestOllamaKeepAliveFollowsTheModelSet checks that the keep-alive window
// can be changed at runtime.
func TestOllamaKeepAliveFollowsTheModelSet(t *testing.T) {
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

	models := NewModels("m", "m", "embed", 4096, "30s")
	p := NewOllama(Options{URL: srv.URL, Models: models, Timeout: 5 * time.Second})
	ch, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for range ch {
	}

	mu.Lock()
	got := body["keep_alive"]
	mu.Unlock()
	if got != "30s" {
		t.Errorf("keep_alive = %v, want %q", got, "30s")
	}
}

// TestOllamaUsesTheModelSet checks that the model name is taken from the
// model set, so the model picker can switch it while the server runs.
func TestOllamaUsesTheModelSet(t *testing.T) {
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

	models := NewModels("first", "first", "embed", 4096, DefaultKeepAlive)
	p := NewOllama(Options{URL: srv.URL, Model: "stale", Models: models, Timeout: 5 * time.Second})

	ch, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for range ch {
	}
	mu.Lock()
	first := body["model"]
	mu.Unlock()
	if first != "first" {
		t.Errorf("model = %v, want %q", first, "first")
	}

	models.Set("second", "second", "embed")

	ch, err = p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for range ch {
	}
	mu.Lock()
	second := body["model"]
	mu.Unlock()
	if second != "second" {
		t.Errorf("model = %v, want %q", second, "second")
	}
}

// TestOllamaEmbedSendsKeepAlive checks that embedding requests release the
// embedding model too.
func TestOllamaEmbedSendsKeepAlive(t *testing.T) {
	var mu sync.Mutex
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(raw, &body)
		mu.Unlock()
		io.WriteString(w, "{\"embeddings\":[[0.1,0.2]]}")
	}))
	defer srv.Close()

	p := NewOllama(Options{URL: srv.URL, Model: "m", EmbedModel: "embed", Timeout: 5 * time.Second})
	if _, err := p.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	mu.Lock()
	got := body["keep_alive"]
	mu.Unlock()
	if got != DefaultKeepAlive {
		t.Errorf("keep_alive = %v, want %q", got, DefaultKeepAlive)
	}
}

// TestOllamaListModels checks the model listing the picker is built on.
func TestOllamaListModels(t *testing.T) {
	const tags = "{\"models\":[" +
		"{\"name\":\"qwen3.5:4b\",\"size\":3300000000,\"modified_at\":\"2026-01-02T03:04:05Z\"," +
		" \"details\":{\"family\":\"qwen3\",\"parameter_size\":\"4.0B\",\"quantization_level\":\"Q4_K_M\"}}," +
		"{\"name\":\"embeddinggemma\",\"size\":300000000,\"details\":{\"family\":\"gemma\"}}]}"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		io.WriteString(w, tags)
	}))
	defer srv.Close()

	p := NewOllama(Options{URL: srv.URL, Model: "m", Timeout: 5 * time.Second})
	infos, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d models, want 2", len(infos))
	}
	if infos[0].Name != "qwen3.5:4b" {
		t.Errorf("name = %q, want %q", infos[0].Name, "qwen3.5:4b")
	}
	if infos[0].Size != 3300000000 {
		t.Errorf("size = %d, want %d", infos[0].Size, int64(3300000000))
	}
	if infos[0].SizeText != "3.1GB" {
		t.Errorf("sizeText = %q, want %q", infos[0].SizeText, "3.1GB")
	}
	if infos[0].Family != "qwen3" {
		t.Errorf("family = %q, want %q", infos[0].Family, "qwen3")
	}
	if infos[0].ParameterSize != "4.0B" {
		t.Errorf("parameterSize = %q, want %q", infos[0].ParameterSize, "4.0B")
	}
	if infos[0].QuantizationLevel != "Q4_K_M" {
		t.Errorf("quantizationLevel = %q, want %q", infos[0].QuantizationLevel, "Q4_K_M")
	}
	if infos[0].ModifiedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("modifiedAt = %q", infos[0].ModifiedAt)
	}
}

// TestOllamaUnload checks that unloading asks Ollama for a zero keep-alive.
func TestOllamaUnload(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		io.WriteString(w, "{}")
	}))
	defer srv.Close()

	p := NewOllama(Options{URL: srv.URL, Model: "m", Timeout: 5 * time.Second})
	if err := p.Unload(context.Background(), []string{"qwen3.5:4b"}); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("got %d requests, want 1", len(bodies))
	}
	if bodies[0]["model"] != "qwen3.5:4b" {
		t.Errorf("model = %v", bodies[0]["model"])
	}
	if bodies[0]["keep_alive"] != float64(0) {
		t.Errorf("keep_alive = %v, want 0", bodies[0]["keep_alive"])
	}
}

// TestModelsSetKeepsTheOtherRoles checks that an empty name leaves the
// other roles alone.
func TestModelsSetKeepsTheOtherRoles(t *testing.T) {
	m := NewModels("chat", "scan", "embed", 4096, DefaultKeepAlive)
	m.Set("", "new-scan", "")
	if m.Chat() != "chat" {
		t.Errorf("chat = %q, want %q", m.Chat(), "chat")
	}
	if m.Scan() != "new-scan" {
		t.Errorf("scan = %q, want %q", m.Scan(), "new-scan")
	}
	if m.Embed() != "embed" {
		t.Errorf("embed = %q, want %q", m.Embed(), "embed")
	}
}

// TestModelsDefaults checks the defaults of a model set.
func TestModelsDefaults(t *testing.T) {
	m := NewModels("chat", "", "", 0, "")
	if m.Scan() != "chat" {
		t.Errorf("scan = %q, want %q", m.Scan(), "chat")
	}
	if m.Embed() != DefaultEmbedModel {
		t.Errorf("embed = %q, want %q", m.Embed(), DefaultEmbedModel)
	}
	if m.NumCtx() != DefaultNumCtx {
		t.Errorf("numCtx = %d, want %d", m.NumCtx(), DefaultNumCtx)
	}
	if m.KeepAlive() != DefaultKeepAlive {
		t.Errorf("keepAlive = %q, want %q", m.KeepAlive(), DefaultKeepAlive)
	}
}

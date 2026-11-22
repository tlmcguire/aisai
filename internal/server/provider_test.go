package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/llm"
)

// TestSetProviderSwitchesToOpenAICompatible switches a running server from
// Ollama to a vLLM-style server and checks that the provider list follows.
func TestSetProviderSwitchesToOpenAICompatible(t *testing.T) {
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen2.5-coder"}]}`))
	}))
	defer vllm.Close()

	cfg := &config.Config{Workspace: t.TempDir(), NumCtx: 4096}
	sw := llm.NewSwitchable(llm.NewOllama(llm.Options{URL: "http://127.0.0.1:1"}), llm.KindOllama, "http://127.0.0.1:1")
	s := New(Options{Config: cfg, LLM: sw})
	h := s.Handler()

	rec, out := do(t, h, http.MethodPost, "/api/providers",
		`{"kind":"vllm","url":"`+vllm.URL+`","model":"qwen2.5-coder"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	info, _ := out["provider"].(map[string]any)
	if info["kind"] != "vllm" || info["ok"] != true {
		t.Fatalf("provider = %+v", info)
	}
	if s.Models().Chat() != "qwen2.5-coder" {
		t.Errorf("chat model = %q", s.Models().Chat())
	}
	if cfg.ProviderKind != "vllm" || cfg.BYOKBaseURL != vllm.URL {
		t.Errorf("config not updated: %+v", cfg)
	}

	rec, _ = do(t, h, http.MethodPost, "/api/providers", `{"kind":"bogus","url":"http://x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bogus kind: status %d", rec.Code)
	}

	rec, out = do(t, h, http.MethodPost, "/api/providers/test", `{"kind":"llamacpp","url":"`+vllm.URL+`"}`)
	info, _ = out["provider"].(map[string]any)
	if rec.Code != http.StatusOK || info["ok"] != true {
		t.Errorf("test llamacpp: %d %+v", rec.Code, info)
	}
}

// TestSetProviderHostedNeedsKey checks that a cloud service is refused
// without an API key, and that the key reaches the service as a header.
func TestSetProviderHostedNeedsKey(t *testing.T) {
	var gotKey, gotVersion string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotVersion = r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-5-5"}]}`))
	}))
	defer api.Close()

	cfg := &config.Config{Workspace: t.TempDir()}
	sw := llm.NewSwitchable(llm.NewOllama(llm.Options{URL: "http://127.0.0.1:1"}), llm.KindOllama, "")
	h := New(Options{Config: cfg, LLM: sw}).Handler()

	rec, _ := do(t, h, http.MethodPost, "/api/providers", `{"kind":"anthropic","url":"`+api.URL+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no key: status %d", rec.Code)
	}
	rec, out := do(t, h, http.MethodPost, "/api/providers", `{"kind":"anthropic","url":"`+api.URL+`","apiKey":"sk-ant-test","model":"claude-sonnet-5-5"}`)
	info, _ := out["provider"].(map[string]any)
	if rec.Code != http.StatusOK || info["ok"] != true {
		t.Fatalf("with key: %d %+v", rec.Code, info)
	}
	if gotKey != "sk-ant-test" || gotVersion == "" {
		t.Errorf("headers: key=%q version=%q", gotKey, gotVersion)
	}
	if cfg.BYOKAPIKey != "sk-ant-test" {
		t.Errorf("key not kept in memory")
	}
}

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
)

// fakeModelProvider is a provider that lists models and can release them.
type fakeModelProvider struct {
	models   []model.ModelInfo
	unloaded []string
}

func (f *fakeModelProvider) ID() string   { return "ollama" }
func (f *fakeModelProvider) Kind() string { return "ollama" }
func (f *fakeModelProvider) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, nil
}
func (f *fakeModelProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.Completion, error) {
	return nil, nil
}
func (f *fakeModelProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, nil
}
func (f *fakeModelProvider) Models(ctx context.Context) ([]string, error) {
	out := make([]string, 0, len(f.models))
	for _, m := range f.models {
		out = append(out, m.Name)
	}
	return out, nil
}
func (f *fakeModelProvider) Health(ctx context.Context) error { return nil }
func (f *fakeModelProvider) ListModels(ctx context.Context) ([]model.ModelInfo, error) {
	return f.models, nil
}
func (f *fakeModelProvider) Unload(ctx context.Context, names []string) error {
	f.unloaded = append(f.unloaded, names...)
	return nil
}

// newModelTestServer builds a server with a fake provider and a
// configuration file on disk.
func newModelTestServer(t *testing.T) (*Server, http.Handler, *fakeModelProvider, string) {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	provider := &fakeModelProvider{models: []model.ModelInfo{
		{Name: "qwen3.5:4b", Size: 3300000000, SizeText: "3.1GB", Family: "qwen3"},
		{Name: "embeddinggemma", Size: 300000000, SizeText: "287.0MB", Family: "gemma"},
	}}

	s := New(Options{Config: cfg, LLM: provider})
	return s, s.Handler(), provider, cfgPath
}

func TestListModelsReportsInstalledAndSelected(t *testing.T) {
	_, h, _, _ := newModelTestServer(t)

	rec, body := do(t, h, "GET", "/api/models", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	models, _ := body["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	first, _ := models[0].(map[string]any)
	if first["name"] != "qwen3.5:4b" {
		t.Errorf("name = %v, want %q", first["name"], "qwen3.5:4b")
	}
	if body["chatModel"] != config.DefaultChatModel {
		t.Errorf("chatModel = %v, want %q", body["chatModel"], config.DefaultChatModel)
	}
	if body["keepAlive"] != config.DefaultKeepAlive {
		t.Errorf("keepAlive = %v, want %q", body["keepAlive"], config.DefaultKeepAlive)
	}
}

func TestSetModelsAppliesAndPersists(t *testing.T) {
	s, h, _, cfgPath := newModelTestServer(t)

	body := "{\"chatModel\":\"qwen3.5:4b\",\"scanModel\":\"qwen3.5:4b\"," +
		"\"embedModel\":\"embeddinggemma\",\"numCtx\":4096,\"keepAlive\":\"90s\"}"
	rec, out := do(t, h, "POST", "/api/models", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if out["chatModel"] != "qwen3.5:4b" {
		t.Errorf("chatModel = %v", out["chatModel"])
	}
	if out["numCtx"] != float64(4096) {
		t.Errorf("numCtx = %v, want 4096", out["numCtx"])
	}
	if out["keepAlive"] != "90s" {
		t.Errorf("keepAlive = %v, want 90s", out["keepAlive"])
	}

	// The change must be visible to the running server.
	if s.Models().Chat() != "qwen3.5:4b" {
		t.Errorf("runtime chat model = %q", s.Models().Chat())
	}

	// And it must survive a restart.
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("parse saved config: %v", err)
	}
	if saved["chatModel"] != "qwen3.5:4b" {
		t.Errorf("saved chatModel = %v", saved["chatModel"])
	}
	if saved["keepAlive"] != "90s" {
		t.Errorf("saved keepAlive = %v", saved["keepAlive"])
	}

	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.ChatModel != "qwen3.5:4b" {
		t.Errorf("reloaded chatModel = %q", reloaded.ChatModel)
	}
}

func TestSetModelsKeepsUnnamedRoles(t *testing.T) {
	s, h, _, _ := newModelTestServer(t)

	rec, _ := do(t, h, "POST", "/api/models", "{\"scanModel\":\"only-scan\"}")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if s.Models().Scan() != "only-scan" {
		t.Errorf("scan = %q, want %q", s.Models().Scan(), "only-scan")
	}
	if s.Models().Chat() != config.DefaultChatModel {
		t.Errorf("chat = %q, want %q", s.Models().Chat(), config.DefaultChatModel)
	}
}

func TestUnloadModelsReleasesTheConfiguredModels(t *testing.T) {
	_, h, provider, _ := newModelTestServer(t)

	rec, out := do(t, h, "POST", "/api/models/unload", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	unloaded, _ := out["unloaded"].([]any)
	if len(unloaded) != 3 {
		t.Fatalf("unloaded %v, want three models", unloaded)
	}
	if len(provider.unloaded) != 3 {
		t.Fatalf("provider unloaded %v, want three", provider.unloaded)
	}
}

func TestUnloadModelsAcceptsAnExplicitList(t *testing.T) {
	_, h, provider, _ := newModelTestServer(t)

	rec, _ := do(t, h, "POST", "/api/models/unload", "{\"models\":[\"qwen3.5:4b\"]}")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(provider.unloaded) != 1 || provider.unloaded[0] != "qwen3.5:4b" {
		t.Fatalf("provider unloaded %v", provider.unloaded)
	}
}

// The server must never write the API key to the configuration file.
func TestSaveNeverWritesTheAPIKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.BYOKAPIKey = "super-secret"

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "super-secret") {
		t.Fatalf("the API key was written to %s", path)
	}
}

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configEnvVars is every environment variable the configuration reads.
//
// A test that does not clear all of them inherits whatever the developer's
// shell happens to export, which makes the test machine-dependent. Clearing
// them (an empty value is indistinguishable from an unset one for the
// configuration code, which tests every value against "") keeps the tests
// hermetic.
var configEnvVars = []string{
	"AISAI_ADVISORY_OFFLINE",
	"AISAI_BYOK_API_KEY",
	"AISAI_BYOK_BASE_URL",
	"AISAI_BYOK_MODEL",
	"AISAI_CHAT_MODEL",
	"AISAI_CONFIG",
	"AISAI_EMBED_MODEL",
	"AISAI_NUM_CTX",
	"AISAI_OLLAMA_URL",
	"AISAI_OLLAMA_URLS",
	"AISAI_OSV_DB",
	"AISAI_SCAN_MODEL",
	"AISAI_SCAN_WORKERS",
	"AISAI_SOCKET",
	"AISAI_WORKSPACE",
	"DISABLE_LLM",
	"OLLAMA_BASE_URL",
	"OLLAMA_PRIMARY_MODEL",
	"OLLAMA_URL",
}

// isolateEnv clears every configuration environment variable and points the
// home directory at a temporary one, so the derived default workspace lands
// inside the test's temporary directory instead of the developer's.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvVars {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// writeConfigFile writes a config file into a temporary directory and returns
// its path.
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}

func TestDefaultReturnsBuiltInDefaults(t *testing.T) {
	isolateEnv(t)

	cfg := Default()
	if cfg.ChatModel != DefaultChatModel {
		t.Errorf("ChatModel = %q, want %q", cfg.ChatModel, DefaultChatModel)
	}
	if cfg.EmbedModel != DefaultEmbedModel {
		t.Errorf("EmbedModel = %q, want %q", cfg.EmbedModel, DefaultEmbedModel)
	}
	if cfg.ScanModel != DefaultChatModel {
		t.Errorf("ScanModel = %q, want %q", cfg.ScanModel, DefaultChatModel)
	}
	if cfg.NumCtx != DefaultNumCtx {
		t.Errorf("NumCtx = %d, want %d", cfg.NumCtx, DefaultNumCtx)
	}
	if cfg.ScanWorkers != DefaultScanWorkers {
		t.Errorf("ScanWorkers = %d, want %d", cfg.ScanWorkers, DefaultScanWorkers)
	}
	if len(cfg.OllamaURLs) != 1 || cfg.OllamaURLs[0] != defaultOllamaURL {
		t.Errorf("OllamaURLs = %v, want [%s]", cfg.OllamaURLs, defaultOllamaURL)
	}
	if cfg.Workspace == "" {
		t.Error("Workspace must not be empty")
	}
	if cfg.BYOKAPIKey != "" {
		t.Errorf("BYOKAPIKey = %q, want empty", cfg.BYOKAPIKey)
	}
}

func TestDefaultConfigPathUsesAISAIConfigEnv(t *testing.T) {
	isolateEnv(t)

	want := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv("AISAI_CONFIG", want)
	if got := DefaultConfigPath(); got != want {
		t.Fatalf("DefaultConfigPath() = %q, want %q", got, want)
	}
}

func TestDefaultConfigPathEndsWithConfigJSON(t *testing.T) {
	isolateEnv(t)

	got := DefaultConfigPath()
	if got == "" {
		t.Fatal("DefaultConfigPath() must not be empty")
	}
	if filepath.Base(got) != "config.json" {
		t.Fatalf("DefaultConfigPath() = %q, want it to end with config.json", got)
	}
}

func TestLoadMissingFileFallsBackToDefaults(t *testing.T) {
	isolateEnv(t)

	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load of a missing file must not fail: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned no config")
	}
	if cfg.ChatModel != DefaultChatModel || cfg.NumCtx != DefaultNumCtx {
		t.Fatalf("Load did not fall back to the defaults: %+v", cfg)
	}
}

func TestLoadEmptyPathReadsTheAISAIConfigFile(t *testing.T) {
	isolateEnv(t)

	path := writeConfigFile(t, `{"chatModel":"custom-chat","numCtx":4096}`)
	t.Setenv("AISAI_CONFIG", path)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatModel != "custom-chat" {
		t.Errorf("ChatModel = %q, want %q", cfg.ChatModel, "custom-chat")
	}
	if cfg.NumCtx != 4096 {
		t.Errorf("NumCtx = %d, want 4096", cfg.NumCtx)
	}
}

func TestLoadEmptyPathWithMissingConfigFallsBackToDefaults(t *testing.T) {
	isolateEnv(t)

	t.Setenv("AISAI_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatModel != DefaultChatModel {
		t.Fatalf("ChatModel = %q, want the default %q", cfg.ChatModel, DefaultChatModel)
	}
}

// TestLoadCannotReadTheAPIKeyFromTheFile pins the fact that the BYOK API key
// is environment-only: the field is tagged `json:"-"`, so a config file that
// carries it is rejected as an unknown field rather than silently ignored.
//
// This is a property of the current implementation, not a wish: the key must
// never be written to disk.
func TestLoadCannotReadTheAPIKeyFromTheFile(t *testing.T) {
	isolateEnv(t)

	path := writeConfigFile(t, `{"byokApiKey":"sk-secret"}`)
	cfg, err := Load(path)
	if err == nil {
		t.Fatalf("Load must reject a config file that carries an API key, got %+v", cfg)
	}
	if !strings.Contains(err.Error(), "byokApiKey") {
		t.Fatalf("error = %v, want it to name the unknown field", err)
	}
	if cfg != nil {
		t.Fatalf("Load must not return a config on error, got %+v", cfg)
	}
}

func TestLoadReadsFileValues(t *testing.T) {
	isolateEnv(t)

	path := writeConfigFile(t, `{
		"workspace": "/tmp/aisai-workspace",
		"disableLlm": true,
		"ollamaUrls": ["http://a:11434", "http://b:11434"],
		"chatModel": "chat",
		"embedModel": "embed",
		"scanModel": "scan",
		"numCtx": 2048,
		"scanWorkers": 7,
		"byokBaseUrl": "https://api.example.com/v1",
		"byokModel": "gpt-4o-mini",
		"advisoryOffline": true,
		"osvDbPath": "/tmp/osv",
		"socketPath": "/tmp/aisai.sock"
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workspace != "/tmp/aisai-workspace" {
		t.Errorf("Workspace = %q", cfg.Workspace)
	}
	if !cfg.DisableLLM {
		t.Error("DisableLLM must be true")
	}
	if len(cfg.OllamaURLs) != 2 || cfg.OllamaURLs[0] != "http://a:11434" {
		t.Errorf("OllamaURLs = %v", cfg.OllamaURLs)
	}
	if cfg.ChatModel != "chat" || cfg.EmbedModel != "embed" || cfg.ScanModel != "scan" {
		t.Errorf("models = %q %q %q", cfg.ChatModel, cfg.EmbedModel, cfg.ScanModel)
	}
	if cfg.NumCtx != 2048 || cfg.ScanWorkers != 7 {
		t.Errorf("NumCtx = %d, ScanWorkers = %d", cfg.NumCtx, cfg.ScanWorkers)
	}
	if cfg.BYOKBaseURL != "https://api.example.com/v1" || cfg.BYOKModel != "gpt-4o-mini" {
		t.Errorf("BYOK = %q %q", cfg.BYOKBaseURL, cfg.BYOKModel)
	}
	if !cfg.AdvisoryOffline || cfg.OSVDBPath != "/tmp/osv" {
		t.Errorf("advisory = %v %q", cfg.AdvisoryOffline, cfg.OSVDBPath)
	}
	if cfg.SocketPath != "/tmp/aisai.sock" {
		t.Errorf("SocketPath = %q", cfg.SocketPath)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	isolateEnv(t)

	path := writeConfigFile(t, `{"notAField":true}`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load must reject an unknown field")
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	isolateEnv(t)

	path := writeConfigFile(t, `{"chatModel":`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load must reject malformed JSON")
	}
}

func TestLoadRejectsAnInvalidConfig(t *testing.T) {
	isolateEnv(t)

	path := writeConfigFile(t, `{"numCtx":0}`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load must reject a config with a non-positive numCtx")
	}
}

func TestLoadEnvironmentOverridesFile(t *testing.T) {
	isolateEnv(t)

	path := writeConfigFile(t, `{"numCtx":4096,"chatModel":"from-file"}`)
	t.Setenv("AISAI_NUM_CTX", "1024")
	t.Setenv("AISAI_CHAT_MODEL", "from-env")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NumCtx != 1024 {
		t.Errorf("NumCtx = %d, want the environment value 1024", cfg.NumCtx)
	}
	if cfg.ChatModel != "from-env" {
		t.Errorf("ChatModel = %q, want the environment value %q", cfg.ChatModel, "from-env")
	}
}

func TestApplyEnvReadsTheOllamaURLVariables(t *testing.T) {
	isolateEnv(t)

	t.Run("OLLAMA_URL", func(t *testing.T) {
		t.Setenv("OLLAMA_URL", "http://only:11434")
		cfg := Default()
		cfg.applyEnv()
		if len(cfg.OllamaURLs) != 1 || cfg.OllamaURLs[0] != "http://only:11434" {
			t.Fatalf("OllamaURLs = %v", cfg.OllamaURLs)
		}
	})

	t.Run("OLLAMA_BASE_URL_wins_over_OLLAMA_URL", func(t *testing.T) {
		t.Setenv("OLLAMA_URL", "http://low:11434")
		t.Setenv("OLLAMA_BASE_URL", "http://high:11434")
		cfg := Default()
		cfg.applyEnv()
		if len(cfg.OllamaURLs) != 1 || cfg.OllamaURLs[0] != "http://high:11434" {
			t.Fatalf("OllamaURLs = %v", cfg.OllamaURLs)
		}
	})

	t.Run("AISAI_OLLAMA_URL_wins_over_OLLAMA_BASE_URL", func(t *testing.T) {
		t.Setenv("OLLAMA_BASE_URL", "http://base:11434")
		t.Setenv("AISAI_OLLAMA_URL", "http://aisai:11434")
		cfg := Default()
		cfg.applyEnv()
		if len(cfg.OllamaURLs) != 1 || cfg.OllamaURLs[0] != "http://aisai:11434" {
			t.Fatalf("OllamaURLs = %v", cfg.OllamaURLs)
		}
	})

	t.Run("AISAI_OLLAMA_URLS_wins_and_is_split", func(t *testing.T) {
		t.Setenv("AISAI_OLLAMA_URL", "http://aisai:11434")
		t.Setenv("AISAI_OLLAMA_URLS", "http://a:11434, http://b:11434 ,")
		cfg := Default()
		cfg.applyEnv()
		want := []string{"http://a:11434", "http://b:11434"}
		if len(cfg.OllamaURLs) != len(want) {
			t.Fatalf("OllamaURLs = %v, want %v", cfg.OllamaURLs, want)
		}
		for i := range want {
			if cfg.OllamaURLs[i] != want[i] {
				t.Fatalf("OllamaURLs = %v, want %v", cfg.OllamaURLs, want)
			}
		}
	})
}

func TestApplyEnvReadsTheModelVariables(t *testing.T) {
	isolateEnv(t)

	t.Setenv("OLLAMA_PRIMARY_MODEL", "legacy-chat")
	t.Setenv("AISAI_SCAN_MODEL", "scan-model")
	t.Setenv("AISAI_EMBED_MODEL", "embed-model")

	cfg := Default()
	cfg.applyEnv()
	if cfg.ChatModel != "legacy-chat" {
		t.Errorf("ChatModel = %q, want %q", cfg.ChatModel, "legacy-chat")
	}
	if cfg.ScanModel != "scan-model" {
		t.Errorf("ScanModel = %q, want %q", cfg.ScanModel, "scan-model")
	}
	if cfg.EmbedModel != "embed-model" {
		t.Errorf("EmbedModel = %q, want %q", cfg.EmbedModel, "embed-model")
	}
}

func TestApplyEnvReadsTheNumericVariables(t *testing.T) {
	isolateEnv(t)

	t.Setenv("AISAI_NUM_CTX", "1234")
	t.Setenv("AISAI_SCAN_WORKERS", "9")

	cfg := Default()
	cfg.applyEnv()
	if cfg.NumCtx != 1234 {
		t.Errorf("NumCtx = %d, want 1234", cfg.NumCtx)
	}
	if cfg.ScanWorkers != 9 {
		t.Errorf("ScanWorkers = %d, want 9", cfg.ScanWorkers)
	}
}

func TestApplyEnvIgnoresUnusableNumericValues(t *testing.T) {
	isolateEnv(t)

	t.Setenv("AISAI_NUM_CTX", "not-a-number")
	t.Setenv("AISAI_SCAN_WORKERS", "0")

	cfg := Default()
	cfg.applyEnv()
	if cfg.NumCtx != DefaultNumCtx {
		t.Errorf("NumCtx = %d, want the default %d", cfg.NumCtx, DefaultNumCtx)
	}
	if cfg.ScanWorkers != DefaultScanWorkers {
		t.Errorf("ScanWorkers = %d, want the default %d", cfg.ScanWorkers, DefaultScanWorkers)
	}
}

func TestApplyEnvReadsDisableLLM(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"1", true},
		{"yes", true},
		{"false", false},
		{"0", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("DISABLE_LLM", tc.value)

			cfg := Default()
			cfg.applyEnv()
			if cfg.DisableLLM != tc.want {
				t.Fatalf("DisableLLM = %v, want %v", cfg.DisableLLM, tc.want)
			}
		})
	}
}

func TestApplyEnvReadsTheBYOKCredentials(t *testing.T) {
	isolateEnv(t)

	t.Setenv("AISAI_BYOK_BASE_URL", "https://api.example.com/v1")
	t.Setenv("AISAI_BYOK_MODEL", "gpt-4o-mini")
	t.Setenv("AISAI_BYOK_API_KEY", "sk-secret-value")

	cfg := Default()
	cfg.applyEnv()
	if cfg.BYOKBaseURL != "https://api.example.com/v1" {
		t.Errorf("BYOKBaseURL = %q", cfg.BYOKBaseURL)
	}
	if cfg.BYOKModel != "gpt-4o-mini" {
		t.Errorf("BYOKModel = %q", cfg.BYOKModel)
	}
	if cfg.BYOKAPIKey != "sk-secret-value" {
		t.Errorf("BYOKAPIKey = %q", cfg.BYOKAPIKey)
	}
}

func TestApplyEnvReadsTheAdvisoryVariables(t *testing.T) {
	isolateEnv(t)

	t.Setenv("AISAI_ADVISORY_OFFLINE", "true")
	t.Setenv("AISAI_OSV_DB", "/tmp/osv-db")
	t.Setenv("AISAI_SOCKET", "/tmp/aisai.sock")
	t.Setenv("AISAI_WORKSPACE", "/tmp/aisai-workspace")

	cfg := Default()
	cfg.applyEnv()
	if !cfg.AdvisoryOffline {
		t.Error("AdvisoryOffline must be true")
	}
	if cfg.OSVDBPath != "/tmp/osv-db" {
		t.Errorf("OSVDBPath = %q", cfg.OSVDBPath)
	}
	if cfg.SocketPath != "/tmp/aisai.sock" {
		t.Errorf("SocketPath = %q", cfg.SocketPath)
	}
	if cfg.Workspace != "/tmp/aisai-workspace" {
		t.Errorf("Workspace = %q", cfg.Workspace)
	}
}

func TestApplyEnvLeavesTheDefaultsAloneWhenNothingIsSet(t *testing.T) {
	isolateEnv(t)

	cfg := Default()
	cfg.applyEnv()
	if cfg.ChatModel != DefaultChatModel || cfg.EmbedModel != DefaultEmbedModel {
		t.Fatalf("applyEnv changed the models: %+v", cfg)
	}
	if len(cfg.OllamaURLs) != 1 || cfg.OllamaURLs[0] != defaultOllamaURL {
		t.Fatalf("applyEnv changed the Ollama URLs: %v", cfg.OllamaURLs)
	}
}

func TestValidateRejectsAnEmptyWorkspace(t *testing.T) {
	isolateEnv(t)

	cfg := Default()
	cfg.Workspace = ""
	if err := cfg.validate(); err == nil {
		t.Fatal("validate must reject an empty workspace")
	}
}

func TestValidateRejectsANonPositiveNumCtx(t *testing.T) {
	isolateEnv(t)

	for _, n := range []int{0, -1} {
		cfg := Default()
		cfg.NumCtx = n
		if err := cfg.validate(); err == nil {
			t.Fatalf("validate must reject numCtx = %d", n)
		}
	}
}

func TestValidateAcceptsAValidConfig(t *testing.T) {
	isolateEnv(t)

	cfg := Default()
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate must accept the defaults: %v", err)
	}
}

func TestValidateFillsInTheMissingValues(t *testing.T) {
	isolateEnv(t)

	cfg := Default()
	cfg.Workspace = t.TempDir()
	cfg.NumCtx = 1
	cfg.ScanWorkers = 0
	cfg.OllamaURLs = nil
	cfg.ChatModel = ""
	cfg.ScanModel = ""
	cfg.EmbedModel = ""

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.ScanWorkers != DefaultScanWorkers {
		t.Errorf("ScanWorkers = %d, want %d", cfg.ScanWorkers, DefaultScanWorkers)
	}
	if len(cfg.OllamaURLs) != 1 || cfg.OllamaURLs[0] != defaultOllamaURL {
		t.Errorf("OllamaURLs = %v", cfg.OllamaURLs)
	}
	if cfg.ChatModel != DefaultChatModel {
		t.Errorf("ChatModel = %q, want %q", cfg.ChatModel, DefaultChatModel)
	}
	if cfg.ScanModel != DefaultChatModel {
		t.Errorf("ScanModel = %q, want %q", cfg.ScanModel, DefaultChatModel)
	}
	if cfg.EmbedModel != DefaultEmbedModel {
		t.Errorf("EmbedModel = %q, want %q", cfg.EmbedModel, DefaultEmbedModel)
	}
}

// TestRedactedRemovesTheAPIKey is the security-critical test: the redacted
// configuration is what is handed to the renderer and written into logs and
// error strings, so the key must not survive it in any form.
func TestRedactedRemovesTheAPIKey(t *testing.T) {
	isolateEnv(t)

	const secret = "sk-super-secret-key"
	cfg := Default()
	cfg.BYOKAPIKey = secret
	cfg.BYOKBaseURL = "https://api.example.com/v1"
	cfg.BYOKModel = "gpt-4o-mini"
	cfg.OllamaURLs = []string{"http://a:11434", "http://b:11434"}
	cfg.ChatModel = "chat"
	cfg.EmbedModel = "embed"
	cfg.ScanModel = "scan"
	cfg.NumCtx = 4096

	red := cfg.Redacted()
	if red.BYOKAPIKey != "" {
		t.Fatalf("Redacted().BYOKAPIKey = %q, want it to be empty", red.BYOKAPIKey)
	}

	// The secret must not appear in any serialised form of the redacted
	// config: JSON, the Go syntax form, or the default formatting.
	forms := map[string]string{}
	if data, err := json.Marshal(red); err == nil {
		forms["json"] = string(data)
	} else {
		t.Fatalf("marshal redacted config: %v", err)
	}
	forms["%v"] = fmt.Sprintf("%v", red)
	forms["%+v"] = fmt.Sprintf("%+v", red)
	forms["%#v"] = fmt.Sprintf("%#v", red)
	for name, form := range forms {
		if strings.Contains(form, secret) {
			t.Errorf("the redacted config leaks the API key in its %s form: %s", name, form)
		}
	}

	// Everything else must survive the redaction.
	if red.BYOKBaseURL != cfg.BYOKBaseURL {
		t.Errorf("BYOKBaseURL = %q, want %q", red.BYOKBaseURL, cfg.BYOKBaseURL)
	}
	if red.BYOKModel != cfg.BYOKModel {
		t.Errorf("BYOKModel = %q, want %q", red.BYOKModel, cfg.BYOKModel)
	}
	if red.ChatModel != cfg.ChatModel || red.EmbedModel != cfg.EmbedModel || red.ScanModel != cfg.ScanModel {
		t.Errorf("the model names were changed: %+v", red)
	}
	if red.NumCtx != cfg.NumCtx {
		t.Errorf("NumCtx = %d, want %d", red.NumCtx, cfg.NumCtx)
	}
	if len(red.OllamaURLs) != len(cfg.OllamaURLs) {
		t.Fatalf("OllamaURLs = %v, want %v", red.OllamaURLs, cfg.OllamaURLs)
	}
	for i := range cfg.OllamaURLs {
		if red.OllamaURLs[i] != cfg.OllamaURLs[i] {
			t.Fatalf("OllamaURLs = %v, want %v", red.OllamaURLs, cfg.OllamaURLs)
		}
	}
}

// TestRedactedDoesNotDestroyTheOriginal checks that the redaction is a copy
// and not a mutation: the server still needs the key to talk to the provider.
func TestRedactedDoesNotDestroyTheOriginal(t *testing.T) {
	isolateEnv(t)

	const secret = "sk-super-secret-key"
	cfg := Default()
	cfg.BYOKAPIKey = secret
	cfg.OllamaURLs = []string{"http://a:11434"}

	red := cfg.Redacted()
	if cfg.BYOKAPIKey != secret {
		t.Fatalf("the original lost its API key: %q", cfg.BYOKAPIKey)
	}

	// Mutating the copy must not reach the original.
	red.ChatModel = "mutated"
	red.NumCtx = 1
	red.BYOKBaseURL = "https://mutated.example.com"
	red.OllamaURLs[0] = "http://mutated:11434"
	red.OllamaURLs = append(red.OllamaURLs, "http://extra:11434")

	if cfg.ChatModel != DefaultChatModel {
		t.Errorf("the original ChatModel changed: %q", cfg.ChatModel)
	}
	if cfg.NumCtx != DefaultNumCtx {
		t.Errorf("the original NumCtx changed: %d", cfg.NumCtx)
	}
	if cfg.BYOKBaseURL != "" {
		t.Errorf("the original BYOKBaseURL changed: %q", cfg.BYOKBaseURL)
	}
	if len(cfg.OllamaURLs) != 1 || cfg.OllamaURLs[0] != "http://a:11434" {
		t.Errorf("the original OllamaURLs changed: %v", cfg.OllamaURLs)
	}
}

func TestRedactedOnAConfigWithoutAnAPIKey(t *testing.T) {
	isolateEnv(t)

	cfg := Default()
	red := cfg.Redacted()
	if red.BYOKAPIKey != "" {
		t.Fatalf("BYOKAPIKey = %q", red.BYOKAPIKey)
	}
	if red.ChatModel != cfg.ChatModel {
		t.Errorf("ChatModel = %q, want %q", red.ChatModel, cfg.ChatModel)
	}
}

// TestJSONNeverSerialisesTheAPIKey pins the second half of the defence: the
// field is tagged `json:"-"`, so even the unredacted config cannot be written
// to a log or a response body with the key in it.
func TestJSONNeverSerialisesTheAPIKey(t *testing.T) {
	isolateEnv(t)

	const secret = "sk-super-secret-key"
	cfg := Default()
	cfg.BYOKAPIKey = secret

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("the marshalled config leaks the API key: %s", data)
	}
	if strings.Contains(string(data), "byokApiKey") {
		t.Fatalf("the marshalled config still carries the API key field: %s", data)
	}
}

func TestEnsureWorkspaceCreatesTheDirectory(t *testing.T) {
	isolateEnv(t)

	root := filepath.Join(t.TempDir(), "workspace", "nested")
	cfg := Default()
	cfg.Workspace = root
	if err := cfg.EnsureWorkspace(); err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat workspace: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", root)
	}
}

func TestSplitCSVTrimsAndDropsEmptyEntries(t *testing.T) {
	got := splitCSV(" a , ,b ,, c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("splitCSV = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitCSV = %v, want %v", got, want)
		}
	}
	if len(splitCSV("")) != 0 {
		t.Fatal("splitCSV of an empty string must be empty")
	}
}

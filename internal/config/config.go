// Package config loads the effective AISAI server configuration.
//
// Precedence, lowest to highest: built-in defaults, config file, environment
// variables, command-line flags. Flags are applied by the caller after Load
// returns.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Default locations and values.
const (
	DefaultChatModel   = "gemma3n:e4b"
	DefaultEmbedModel  = "embeddinggemma"
	DefaultNumCtx      = 8192
	DefaultScanWorkers = 4
	defaultOllamaURL   = "http://localhost:11434"
)

// Config is the effective server configuration.
type Config struct {
	// Workspace is the root directory for audit workspaces, indexes and caches.
	Workspace string `json:"workspace"`

	// DisableLLM turns off all LLM-backed features (scan + chat). The legacy
	// app exposed this as DISABLE_LLM; it is honoured for compatibility.
	DisableLLM bool `json:"disableLlm"`

	// OllamaURLs are candidate Ollama endpoints, tried in order.
	OllamaURLs []string `json:"ollamaUrls"`

	ChatModel   string `json:"chatModel"`
	EmbedModel  string `json:"embedModel"`
	ScanModel   string `json:"scanModel"`
	NumCtx      int    `json:"numCtx"`
	ScanWorkers int    `json:"scanWorkers"`

	// BYOK (bring your own key) optional OpenAI-compatible provider.
	BYOKBaseURL string `json:"byokBaseUrl,omitempty"`
	BYOKModel   string `json:"byokModel,omitempty"`
	BYOKAPIKey  string `json:"-"` // never serialized

	// Advisory layer.
	AdvisoryOffline bool   `json:"advisoryOffline"`
	OSVDBPath       string `json:"osvDbPath,omitempty"`

	// SocketPath is the Unix socket / named pipe to listen on.
	SocketPath string `json:"socketPath,omitempty"`
}

// Default returns the built-in defaults.
func Default() *Config {
	return &Config{
		Workspace:   defaultWorkspace(),
		OllamaURLs:  []string{defaultOllamaURL},
		ChatModel:   DefaultChatModel,
		EmbedModel:  DefaultEmbedModel,
		ScanModel:   DefaultChatModel,
		NumCtx:      DefaultNumCtx,
		ScanWorkers: DefaultScanWorkers,
	}
}

// defaultWorkspace picks a per-user data directory that works on macOS,
// Linux and Windows.
func defaultWorkspace() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "AISAI")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".aisai")
	}
	return filepath.Join(os.TempDir(), "aisai")
}

// DefaultConfigPath is where the config file is read from when no explicit
// path is given.
func DefaultConfigPath() string {
	if p := os.Getenv("AISAI_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "AISAI", "config.json")
}

// Load reads the config file (if it exists) and applies environment overrides.
// A missing config file is not an error.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path == "" {
		path = DefaultConfigPath()
	}
	if path != "" {
		if err := cfg.loadFile(path); err != nil {
			return nil, err
		}
	}

	cfg.applyEnv()

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

// applyEnv overlays environment variables. Several legacy names are honoured
// so existing AISAI setups keep working.
func (c *Config) applyEnv() {
	if v := os.Getenv("AISAI_WORKSPACE"); v != "" {
		c.Workspace = v
	}
	if v := os.Getenv("AISAI_OLLAMA_URLS"); v != "" {
		c.OllamaURLs = splitCSV(v)
	} else if v := os.Getenv("AISAI_OLLAMA_URL"); v != "" {
		c.OllamaURLs = []string{v}
	} else if v := os.Getenv("OLLAMA_BASE_URL"); v != "" {
		c.OllamaURLs = []string{v}
	} else if v := os.Getenv("OLLAMA_URL"); v != "" {
		c.OllamaURLs = []string{v}
	}

	if v := os.Getenv("AISAI_CHAT_MODEL"); v != "" {
		c.ChatModel = v
	} else if v := os.Getenv("OLLAMA_PRIMARY_MODEL"); v != "" {
		c.ChatModel = v
	}
	if v := os.Getenv("AISAI_SCAN_MODEL"); v != "" {
		c.ScanModel = v
	}
	if v := os.Getenv("AISAI_EMBED_MODEL"); v != "" {
		c.EmbedModel = v
	}
	if v := os.Getenv("AISAI_NUM_CTX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.NumCtx = n
		}
	}
	if v := os.Getenv("AISAI_SCAN_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.ScanWorkers = n
		}
	}
	if v := os.Getenv("DISABLE_LLM"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.DisableLLM = b
		} else if strings.EqualFold(v, "1") || strings.EqualFold(v, "yes") {
			c.DisableLLM = true
		}
	}
	if v := os.Getenv("AISAI_BYOK_BASE_URL"); v != "" {
		c.BYOKBaseURL = v
	}
	if v := os.Getenv("AISAI_BYOK_MODEL"); v != "" {
		c.BYOKModel = v
	}
	if v := os.Getenv("AISAI_BYOK_API_KEY"); v != "" {
		c.BYOKAPIKey = v
	}
	if v := os.Getenv("AISAI_ADVISORY_OFFLINE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.AdvisoryOffline = b
		}
	}
	if v := os.Getenv("AISAI_OSV_DB"); v != "" {
		c.OSVDBPath = v
	}
	if v := os.Getenv("AISAI_SOCKET"); v != "" {
		c.SocketPath = v
	}
}

func (c *Config) validate() error {
	if c.Workspace == "" {
		return fmt.Errorf("workspace must not be empty")
	}
	if c.NumCtx <= 0 {
		return fmt.Errorf("numCtx must be positive, got %d", c.NumCtx)
	}
	if c.ScanWorkers <= 0 {
		c.ScanWorkers = DefaultScanWorkers
	}
	if len(c.OllamaURLs) == 0 {
		c.OllamaURLs = []string{defaultOllamaURL}
	}
	if c.ChatModel == "" {
		c.ChatModel = DefaultChatModel
	}
	if c.ScanModel == "" {
		c.ScanModel = c.ChatModel
	}
	if c.EmbedModel == "" {
		c.EmbedModel = DefaultEmbedModel
	}
	return nil
}

// SplitCSV splits a comma-separated list, trimming spaces and dropping empties.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// EnsureWorkspace creates the workspace directory if needed.
func (c *Config) EnsureWorkspace() error {
	return os.MkdirAll(c.Workspace, 0o755)
}

// Redacted returns a copy safe to send to a client: the API key is removed.
func (c *Config) Redacted() *Config {
	cp := *c
	cp.BYOKAPIKey = ""
	cp.OllamaURLs = append([]string(nil), c.OllamaURLs...)
	return &cp
}

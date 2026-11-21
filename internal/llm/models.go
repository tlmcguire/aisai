package llm

import (
	"sync"
)

// DefaultKeepAlive is how long Ollama keeps a model resident in memory after
// the last request that used it.
//
// The desktop app runs on laptops with 8GB of unified memory, so a model
// that stays resident steals memory from the rest of the system long after
// the audit that needed it is over. Three minutes is long enough to keep a
// model warm across the steps of one scan and short enough that the memory
// comes back before the user notices.
const DefaultKeepAlive = "3m"

// DefaultEmbedModel is the embedding model used when none is configured.
const DefaultEmbedModel = "embeddinggemma"

// Models holds the model names the server uses.
//
// The values can change while the server is running: the desktop app has a
// model picker that switches the chat, scan and embedding models without a
// restart. Every read therefore goes through a mutex, and every component
// that needs a model name reads it from here rather than caching it at
// construction time.
type Models struct {
	mu        sync.RWMutex
	chat      string
	scan      string
	embed     string
	numCtx    int
	keepAlive string
}

// NewModels returns a model set.
func NewModels(chat, scan, embed string, numCtx int, keepAlive string) *Models {
	m := &Models{
		chat:      chat,
		scan:      scan,
		embed:     embed,
		numCtx:    numCtx,
		keepAlive: keepAlive,
	}
	m.normalize()
	return m
}

// normalize fills in the defaults for the empty fields.
func (m *Models) normalize() {
	if m.scan == "" {
		m.scan = m.chat
	}
	if m.embed == "" {
		m.embed = DefaultEmbedModel
	}
	if m.numCtx <= 0 {
		m.numCtx = DefaultNumCtx
	}
	if m.keepAlive == "" {
		m.keepAlive = DefaultKeepAlive
	}
}

// Chat returns the chat model.
func (m *Models) Chat() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.chat
}

// Scan returns the detection model.
func (m *Models) Scan() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scan
}

// Embed returns the embedding model.
func (m *Models) Embed() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.embed
}

// NumCtx returns the context window sent to the model.
func (m *Models) NumCtx() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.numCtx
}

// KeepAlive returns the keep-alive window sent to Ollama.
func (m *Models) KeepAlive() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.keepAlive
}

// Set replaces the chat, scan and embedding models.
//
// An empty name leaves the current value alone, so a caller can update one
// role without having to read and rewrite the other two.
func (m *Models) Set(chat, scan, embed string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if chat != "" {
		m.chat = chat
	}
	if scan != "" {
		m.scan = scan
	}
	if embed != "" {
		m.embed = embed
	}
}

// SetNumCtx replaces the context window. A non-positive value is ignored.
func (m *Models) SetNumCtx(n int) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.numCtx = n
}

// SetKeepAlive replaces the keep-alive window. An empty value is ignored.
func (m *Models) SetKeepAlive(s string) {
	if s == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keepAlive = s
}

// Snapshot is a consistent copy of the model set.
type Snapshot struct {
	Chat      string `json:"chatModel"`
	Scan      string `json:"scanModel"`
	Embed     string `json:"embedModel"`
	NumCtx    int    `json:"numCtx"`
	KeepAlive string `json:"keepAlive"`
}

// Snapshot returns a consistent copy of the model set.
func (m *Models) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot{
		Chat:      m.chat,
		Scan:      m.scan,
		Embed:     m.embed,
		NumCtx:    m.numCtx,
		KeepAlive: m.keepAlive,
	}
}

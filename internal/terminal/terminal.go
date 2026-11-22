// Package terminal runs interactive shells for the app's terminal panel.
//
// A session is a pseudo-terminal with the user's shell on it. Bytes the user
// types go in, bytes the shell prints come out through a callback; the package
// never interprets either. The shell runs as the user, with the user's
// permissions, exactly like the terminal of any editor: it is a feature for
// the person at the keyboard, which is why the server only starts one on an
// explicit request and refuses to when it is disabled.
package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// MaxSessions is the most shells that can be open at once.
const MaxSessions = 8

// ErrUnsupported is returned on a platform without pseudo-terminal support.
var ErrUnsupported = errors.New("the integrated terminal is not available on this platform yet")

// ErrNotFound is returned for an unknown session.
var ErrNotFound = errors.New("no such terminal")

// Options describe a new session.
type Options struct {
	// Cwd is the directory the shell starts in. It must exist.
	Cwd string
	// Cols and Rows are the size of the terminal in cells.
	Cols, Rows uint16
	// Shell overrides the user's login shell.
	Shell string
}

// Handlers receive what a session produces. Both are called from the
// session's own goroutine.
type Handlers struct {
	// Data receives output. The slice is owned by the callee.
	Data func(id string, data []byte)
	// Exit is called once when the shell ends, with its exit code.
	Exit func(id string, code int)
}

type session interface {
	write([]byte) error
	resize(cols, rows uint16) error
	close()
}

// Manager owns the open sessions.
type Manager struct {
	mu       sync.Mutex
	next     int
	sessions map[string]session
}

// NewManager returns an empty manager.
func NewManager() *Manager { return &Manager{sessions: map[string]session{}} }

// Open starts a shell and returns the id of its session.
func (m *Manager) Open(opts Options, h Handlers) (string, error) {
	if info, err := os.Stat(opts.Cwd); err != nil || !info.IsDir() {
		return "", fmt.Errorf("the directory %q does not exist", opts.Cwd)
	}
	if opts.Cols == 0 {
		opts.Cols = 80
	}
	if opts.Rows == 0 {
		opts.Rows = 24
	}

	m.mu.Lock()
	if len(m.sessions) >= MaxSessions {
		m.mu.Unlock()
		return "", fmt.Errorf("at most %d terminals can be open", MaxSessions)
	}
	m.next++
	id := fmt.Sprintf("t%d", m.next)
	m.mu.Unlock()

	s, err := start(id, opts, Handlers{
		Data: h.Data,
		Exit: func(id string, code int) {
			m.mu.Lock()
			delete(m.sessions, id)
			m.mu.Unlock()
			if h.Exit != nil {
				h.Exit(id, code)
			}
		},
	})
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	return id, nil
}

func (m *Manager) get(id string) (session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

// Write sends keystrokes to a session.
func (m *Manager) Write(id string, data []byte) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	return s.write(data)
}

// Resize changes the size of a session's terminal.
func (m *Manager) Resize(id string, cols, rows uint16) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	return s.resize(cols, rows)
}

// Close ends a session.
func (m *Manager) Close(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	s.close()
	return nil
}

// CloseAll ends every session. The server calls it on shutdown, so no shell
// outlives the app.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	all := make([]session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.close()
	}
}

// shellFor picks the shell to run.
func shellFor(override string) string {
	if override != "" {
		return override
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return "bash"
	}
	return "/bin/sh"
}

// environment is the shell's environment: the user's own, without anything
// that belongs to AISAI (its settings and any key it was handed), and with a
// terminal type that xterm understands.
func environment() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "AISAI_") || strings.HasPrefix(name, "BYOK_") {
			continue
		}
		if name == "TERM" || name == "COLORTERM" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// HomeDir is where a shell starts when no audit is given.
func HomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return filepath.Clean(os.TempDir())
}

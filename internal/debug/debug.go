// Package debug runs a Python program under a debugger for the app's debug
// panel.
//
// The program runs under driver.py, a small debugger built on the standard
// library's bdb, so nothing has to be installed in the project. The driver
// connects back to a loopback socket and speaks one JSON object per line:
// commands go in (setBreakpoints, continue, next, step, out, pause, frame,
// evaluate), events come out (stopped, continued, scope, evaluated,
// breakpoints, exited). The program's own stdout and stderr are read from the
// process.
//
// Running a program runs its code with the user's permissions. The server
// only offers this for a project folder the user opened themselves, never for
// a downloaded package, and only on an explicit request.
package debug

import (
	"bufio"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

//go:embed driver.py
var driverSource []byte

// MaxSessions is the most programs that can be debugged at once.
const MaxSessions = 4

// handshakeTimeout is how long the driver has to connect back.
const handshakeTimeout = 20 * time.Second

// ErrNotFound is returned for an unknown session.
var ErrNotFound = errors.New("no such debug session")

// ErrNoPython is returned when no Python interpreter can be found.
var ErrNoPython = errors.New("no Python interpreter was found: install Python 3, or create a .venv in the project")

// Options describe a program to debug.
type Options struct {
	// Root is the directory every path in commands and events is relative
	// to (the audit workspace).
	Root string
	// Dir is the working directory of the program.
	Dir string
	// Program is the script to run, relative to Root.
	Program string
	// Args are passed to the program.
	Args []string
	// Python is the interpreter. Empty means FindPython(Dir).
	Python string
	// Breakpoints maps a file relative to Root to its breakpoint lines.
	Breakpoints map[string][]int
	// StopOnEntry pauses on the program's first line.
	StopOnEntry bool
	// Env is the environment of the program. Nil means a copy of this
	// process's environment without AISAI's own settings.
	Env []string
}

// Handlers receive what a session produces. They are called from the
// session's goroutines.
type Handlers struct {
	// Event receives a driver event, with its paths made relative to Root.
	Event func(id string, event map[string]any)
	// Output receives what the program writes; stream is "stdout" or
	// "stderr".
	Output func(id, stream, text string)
	// Exit is called once when the program has ended.
	Exit func(id string, code int)
}

// Command is one instruction for a paused or running program.
type Command struct {
	Cmd        string `json:"cmd"`
	File       string `json:"file,omitempty"`
	Lines      []int  `json:"lines,omitempty"`
	Frame      int    `json:"frame"`
	Expression string `json:"expression,omitempty"`
	Seq        int    `json:"seq,omitempty"`
}

var allowedCommands = map[string]bool{
	"setBreakpoints": true, "continue": true, "next": true, "step": true,
	"out": true, "pause": true, "frame": true, "evaluate": true,
}

// Info describes a started session.
type Info struct {
	ID      string `json:"id"`
	Python  string `json:"python"`
	Version string `json:"version,omitempty"`
	Program string `json:"program"`
}

type session struct {
	id   string
	root string
	cmd  *exec.Cmd
	conn net.Conn
	tmp  string

	mu     sync.Mutex
	closed bool
}

// Manager owns the running sessions.
type Manager struct {
	mu       sync.Mutex
	next     int
	sessions map[string]*session
}

// NewManager returns an empty manager.
func NewManager() *Manager { return &Manager{sessions: map[string]*session{}} }

// Start runs a program under the debugger. It returns once the driver has
// connected and the breakpoints are set; the program is running by then.
func (m *Manager) Start(opts Options, h Handlers) (Info, error) {
	program, err := resolve(opts.Root, opts.Program)
	if err != nil {
		return Info{}, err
	}
	if !strings.EqualFold(filepath.Ext(program), ".py") {
		return Info{}, errors.New("only Python files (.py) can be debugged")
	}
	if info, err := os.Stat(program); err != nil || info.IsDir() {
		return Info{}, fmt.Errorf("%s does not exist", opts.Program)
	}
	dir := opts.Dir
	if dir == "" {
		dir = filepath.Dir(program)
	}
	python := opts.Python
	if python == "" {
		if python = FindPython(dir); python == "" {
			return Info{}, ErrNoPython
		}
	}

	m.mu.Lock()
	if len(m.sessions) >= MaxSessions {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("at most %d programs can be debugged at once", MaxSessions)
	}
	m.next++
	id := fmt.Sprintf("d%d", m.next)
	m.mu.Unlock()

	tmp, err := os.MkdirTemp("", "aisai-debug-")
	if err != nil {
		return Info{}, err
	}
	driver := filepath.Join(tmp, "aisai_debug_driver.py")
	if err := os.WriteFile(driver, driverSource, 0o600); err != nil {
		_ = os.RemoveAll(tmp)
		return Info{}, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = os.RemoveAll(tmp)
		return Info{}, err
	}
	defer listener.Close()
	token := randomToken()

	env := opts.Env
	if env == nil {
		env = Environment()
	}
	stopOnEntry := "0"
	if opts.StopOnEntry {
		stopOnEntry = "1"
	}
	env = append(env,
		fmt.Sprintf("AISAI_DEBUG_PORT=%d", listener.Addr().(*net.TCPAddr).Port),
		"AISAI_DEBUG_TOKEN="+token,
		"AISAI_DEBUG_STOP_ON_ENTRY="+stopOnEntry,
		// The copy of the project is not the place for bytecode caches.
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONUNBUFFERED=1",
	)

	args := append([]string{"-u", driver, program}, opts.Args...)
	cmd := exec.Command(python, args...)
	cmd.Dir = dir
	cmd.Env = env
	setProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(tmp)
		return Info{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(tmp)
		return Info{}, err
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(tmp)
		return Info{}, fmt.Errorf("start %s: %w", python, err)
	}

	s := &session{id: id, root: opts.Root, cmd: cmd, tmp: tmp}
	var outputs sync.WaitGroup
	outputs.Add(2)
	go s.pump(&outputs, stdout, "stdout", h)
	go s.pump(&outputs, stderr, "stderr", h)

	// The program ends either way; the wait reports it once its output has
	// been read.
	exited := make(chan struct{})
	go func() {
		outputs.Wait()
		code := 0
		if err := cmd.Wait(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else {
				code = -1
			}
		}
		close(exited)
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		s.shutdown()
		if h.Exit != nil {
			h.Exit(id, code)
		}
	}()

	conn, hello, err := accept(listener, token, exited)
	if err != nil {
		s.kill()
		return Info{}, err
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	for file, lines := range opts.Breakpoints {
		if err := s.send(Command{Cmd: "setBreakpoints", File: file, Lines: lines}); err != nil {
			s.kill()
			return Info{}, err
		}
	}
	reader := bufio.NewReaderSize(conn, 64*1024)
	go s.listen(reader, h)
	if err := s.write(map[string]any{"cmd": "start"}); err != nil {
		s.kill()
		return Info{}, err
	}
	version, _ := hello["python"].(string)
	return Info{ID: id, Python: python, Version: version, Program: model.RelPath(opts.Program)}, nil
}

// accept waits for the driver to connect and checks its token.
func accept(listener net.Listener, token string, exited <-chan struct{}) (net.Conn, map[string]any, error) {
	type result struct {
		conn  net.Conn
		hello map[string]any
		err   error
	}
	done := make(chan result, 1)
	go func() {
		if tcp, ok := listener.(*net.TCPListener); ok {
			_ = tcp.SetDeadline(time.Now().Add(handshakeTimeout))
		}
		conn, err := listener.Accept()
		if err != nil {
			done <- result{err: errors.New("the Python debugger did not start in time")}
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		_ = conn.SetReadDeadline(time.Time{})
		var hello map[string]any
		if err != nil || json.Unmarshal(line, &hello) != nil || hello["event"] != "hello" || hello["token"] != token {
			_ = conn.Close()
			done <- result{err: errors.New("the debugger connection could not be verified")}
			return
		}
		done <- result{conn: conn, hello: hello}
	}()
	select {
	case r := <-done:
		return r.conn, r.hello, r.err
	case <-exited:
		_ = listener.Close()
		return nil, nil, errors.New("Python exited before the debugger started; see the output for why")
	}
}

// Send passes a command to a session.
func (m *Manager) Send(id string, c Command) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if !allowedCommands[c.Cmd] {
		return fmt.Errorf("unknown debugger command %q", c.Cmd)
	}
	return s.send(c)
}

// Stop ends a session's program.
func (m *Manager) Stop(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	s.kill()
	return nil
}

// CloseAll ends every program. The server calls it on shutdown.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	all := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.kill()
	}
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

// send translates a command's file to an absolute path and writes it.
func (s *session) send(c Command) error {
	msg := map[string]any{"cmd": c.Cmd, "frame": c.Frame}
	switch c.Cmd {
	case "setBreakpoints":
		abs, err := resolve(s.root, c.File)
		if err != nil {
			return err
		}
		lines := c.Lines
		if lines == nil {
			lines = []int{}
		}
		msg["file"], msg["lines"] = abs, lines
	case "evaluate":
		msg["expression"], msg["seq"] = c.Expression, c.Seq
	}
	return s.write(msg)
}

func (s *session) write(msg map[string]any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.conn == nil {
		return ErrNotFound
	}
	_, err = s.conn.Write(append(data, '\n'))
	return err
}

// listen forwards driver events with their paths made relative to the root.
func (s *session) listen(r *bufio.Reader, h Handlers) {
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var ev map[string]any
			if json.Unmarshal(line, &ev) == nil {
				s.relativize(ev)
				if h.Event != nil {
					h.Event(s.id, ev)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *session) relativize(ev map[string]any) {
	if file, ok := ev["file"].(string); ok {
		rel, external := relative(s.root, file)
		ev["file"] = rel
		if external {
			ev["external"] = true
		}
	}
	frames, _ := ev["frames"].([]any)
	for _, f := range frames {
		frame, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if file, ok := frame["file"].(string); ok {
			rel, external := relative(s.root, file)
			frame["file"] = rel
			if external {
				frame["external"] = true
			}
		}
	}
}

func (s *session) pump(wg *sync.WaitGroup, r io.Reader, stream string, h Handlers) {
	defer wg.Done()
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 && h.Output != nil {
			h.Output(s.id, stream, string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (s *session) kill() {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	killProcess(s.cmd)
	s.shutdown()
}

func (s *session) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.conn != nil {
		_ = s.conn.Close()
	}
	_ = os.RemoveAll(s.tmp)
}

// resolve joins a root-relative path to the root and refuses anything that
// escapes it.
func resolve(root, rel string) (string, error) {
	clean := model.RelPath(rel)
	if clean == "" {
		return "", errors.New("a file is required")
	}
	rootAbs := filepath.Clean(root)
	abs := filepath.Join(rootAbs, filepath.FromSlash(clean))
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(os.PathSeparator)) {
		return "", errors.New("path escapes the audit workspace")
	}
	return abs, nil
}

// relative turns a path the driver reports into one relative to the root.
// A file outside the root (the standard library, an installed package) keeps
// its absolute path and is marked external.
func relative(root, abs string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return abs, true
	}
	return filepath.ToSlash(rel), false
}

// FindPython picks the interpreter for a project: its own virtual
// environment when one of the usual names exists in dir or a parent of it
// (up to four levels), and python3 or python on the PATH otherwise.
func FindPython(dirs ...string) string {
	bin, exe := "bin", "python"
	if runtime.GOOS == "windows" {
		bin, exe = "Scripts", "python.exe"
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for d, i := filepath.Clean(dir), 0; i < 4; d, i = filepath.Dir(d), i+1 {
			for _, venv := range []string{".venv", "venv", "env"} {
				p := filepath.Join(d, venv, bin, exe)
				if info, err := os.Stat(p); err == nil && !info.IsDir() {
					return p
				}
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// Environment is the program's environment: the user's own, without anything
// that belongs to AISAI (its settings and any key it was handed).
func Environment() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "AISAI_") || strings.HasPrefix(name, "BYOK_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

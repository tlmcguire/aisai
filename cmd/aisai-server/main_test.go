package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/version"
)

// captureStdout runs fn with os.Stdout replaced by a pipe and returns
// everything that was written to it.
//
// The pipe is drained by a goroutine so that a writer cannot block on a full
// pipe buffer.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()

	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestMainPrintsTheVersionAndStartsNoServer covers the --version flag.
//
// The version flag must short-circuit the whole startup: the server must not
// listen on a socket, and the only thing on stdout must be the version line.
func TestMainPrintsTheVersionAndStartsNoServer(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// main() registers its flags on the global flag set, so the set has to
	// be fresh for every invocation: a second call would panic with
	// "flag redefined".
	origFlags := flag.CommandLine
	flag.CommandLine = flag.NewFlagSet("aisai-server", flag.ExitOnError)
	defer func() { flag.CommandLine = origFlags }()

	socket := filepath.Join(t.TempDir(), "aisai.sock")
	os.Args = []string{"aisai-server", "--version", "--socket", socket}

	out := captureStdout(t, func() { main() })

	trimmed := strings.TrimSpace(out)
	if trimmed != version.String() {
		t.Fatalf("stdout = %q, want the version line %q", trimmed, version.String())
	}
	if !strings.Contains(out, version.Short()) {
		t.Errorf("stdout = %q, want it to contain the version %q", out, version.Short())
	}
	if !strings.Contains(out, "commit") {
		t.Errorf("stdout = %q, want it to contain the full version string", out)
	}

	// The server must not have been started: no socket may exist.
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("the server created %s; --version must not start a server", socket)
	}
}

// TestEmitListeningPrintsTheHandshakeLine checks the single JSON line the
// Electron main process parses to learn the listen address.
func TestEmitListeningPrintsTheHandshakeLine(t *testing.T) {
	out := captureStdout(t, func() {
		if err := emitListening("/tmp/aisai-test.sock"); err != nil {
			t.Fatalf("emitListening: %v", err)
		}
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("the handshake line is not JSON: %v (%q)", err, out)
	}
	if payload["type"] != "listening" {
		t.Errorf("type = %v, want %q", payload["type"], "listening")
	}
	if payload["address"] != "/tmp/aisai-test.sock" {
		t.Errorf("address = %v", payload["address"])
	}
	if payload["transport"] != "unix" {
		t.Errorf("transport = %v, want %q", payload["transport"], "unix")
	}
	if payload["version"] != version.Short() {
		t.Errorf("version = %v, want %q", payload["version"], version.Short())
	}
}

// TestEmitListeningReportsTheTCPFallback checks that a loopback address is
// announced as TCP: the Electron side has to know which transport to dial.
func TestEmitListeningReportsTheTCPFallback(t *testing.T) {
	out := captureStdout(t, func() {
		if err := emitListening("127.0.0.1:54321"); err != nil {
			t.Fatalf("emitListening: %v", err)
		}
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("the handshake line is not JSON: %v (%q)", err, out)
	}
	if payload["transport"] != "tcp" {
		t.Errorf("transport = %v, want %q", payload["transport"], "tcp")
	}
	if payload["address"] != "127.0.0.1:54321" {
		t.Errorf("address = %v", payload["address"])
	}
}

// TestListenTakesItsOwnSocketWhenThePathIsTaken covers two copies of AISAI
// on one machine.
//
// The socket path is shared, so the second copy finds it taken. It must take
// a socket of its own: the first copy is still serving, and taking its path
// would leave it unable to answer.
func TestListenTakesItsOwnSocketWhenThePathIsTaken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the server uses a loopback port on Windows")
	}
	// The socket path has to stay short: the system limits it to about a
	// hundred bytes, and the temporary directory can be long.
	path := filepath.Join(os.TempDir(), fmt.Sprintf("aisai-taken-%d.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(path) })

	other, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	defer other.Close()

	addr, listener, err := listen(&config.Config{SocketPath: path})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	if addr == path {
		t.Fatalf("the server took %s, which another instance is serving", path)
	}

	// The first copy must still answer on its own path.
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("the first instance lost its socket: %v", err)
	}
	conn.Close()
}

// TestListenTakesOverAStaleSocket covers the socket of a run that crashed.
//
// Nobody answers on it, so the server may take it over instead of asking the
// person to clean up a file they never knew about.
func TestListenTakesOverAStaleSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the server uses a loopback port on Windows")
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("aisai-stale-%d.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(path) })

	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	// Leave the file behind, the way a crashed run would.
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()

	addr, listener, err := listen(&config.Config{SocketPath: path})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	if addr != path {
		t.Fatalf("address = %q, want the free socket %q", addr, path)
	}
}

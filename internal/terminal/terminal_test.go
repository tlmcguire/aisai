//go:build !windows

package terminal

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestShellRoundTrip(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()

	var mu sync.Mutex
	var out strings.Builder
	exited := make(chan int, 1)
	id, err := m.Open(Options{Cwd: t.TempDir(), Shell: "/bin/sh"}, Handlers{
		Data: func(_ string, b []byte) { mu.Lock(); out.Write(b); mu.Unlock() },
		Exit: func(_ string, code int) { exited <- code },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Write(id, []byte("echo aisai-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := out.String()
		mu.Unlock()
		if strings.Contains(got, "aisai-42") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no output from the shell: %q", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := m.Resize(id, 100, 30); err != nil {
		t.Errorf("resize: %v", err)
	}
	_ = m.Write(id, []byte("exit 3\n"))
	select {
	case code := <-exited:
		if code != 3 {
			t.Errorf("exit code = %d, want 3", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the shell did not exit")
	}
	if err := m.Write(id, []byte("x")); err != ErrNotFound {
		t.Errorf("a finished session must be gone, got %v", err)
	}
}

func TestEnvironmentDropsAisaiVariables(t *testing.T) {
	t.Setenv("AISAI_SECRET", "x")
	t.Setenv("BYOK_API_KEY", "y")
	for _, kv := range environment() {
		if strings.HasPrefix(kv, "AISAI_") || strings.HasPrefix(kv, "BYOK_") {
			t.Errorf("%s leaked into the shell", kv)
		}
	}
}

func TestOpenRefusesAMissingDirectory(t *testing.T) {
	if _, err := NewManager().Open(Options{Cwd: "/definitely/not/here"}, Handlers{}); err == nil {
		t.Error("expected an error")
	}
}

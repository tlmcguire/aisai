package debug

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu     sync.Mutex
	events chan map[string]any
	output strings.Builder
	exit   chan int
}

func newRecorder() *recorder {
	return &recorder{events: make(chan map[string]any, 64), exit: make(chan int, 1)}
}

func (r *recorder) handlers() Handlers {
	return Handlers{
		Event: func(_ string, ev map[string]any) { r.events <- ev },
		Output: func(_, _, text string) {
			r.mu.Lock()
			r.output.WriteString(text)
			r.mu.Unlock()
		},
		Exit: func(_ string, code int) { r.exit <- code },
	}
}

// next returns the next event of the given kind, skipping the others.
func (r *recorder) next(t *testing.T, kind string) map[string]any {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-r.events:
			if ev["event"] == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %s event", kind)
		}
	}
}

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
}

func writeProgram(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proj", "prog.py"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestBreakpointStepEvaluate(t *testing.T) {
	needPython(t)
	root := writeProgram(t, "def add(a, b):\n    c = a + b\n    return c\n\nx = add(1, 2)\nprint('x', x)\n")
	m := NewManager()
	defer m.CloseAll()
	rec := newRecorder()
	info, err := m.Start(Options{
		Root:        root,
		Program:     "proj/prog.py",
		Breakpoints: map[string][]int{"proj/prog.py": {2}},
		Env:         Environment(),
	}, rec.handlers())
	if err != nil {
		t.Fatal(err)
	}

	bp := rec.next(t, "breakpoints")
	if bp["file"] != "proj/prog.py" {
		t.Fatalf("breakpoint file = %v", bp["file"])
	}

	stopped := rec.next(t, "stopped")
	frames := stopped["frames"].([]any)
	top := frames[0].(map[string]any)
	if stopped["reason"] != "breakpoint" || top["file"] != "proj/prog.py" || top["line"] != float64(2) || top["name"] != "add" {
		t.Fatalf("stopped = %v", stopped)
	}
	if len(frames) != 2 {
		t.Fatalf("want the function and the module frame, got %v", frames)
	}
	locals := stopped["locals"].([]any)
	if len(locals) != 2 || locals[0].(map[string]any)["name"] != "a" || locals[0].(map[string]any)["value"] != "1" {
		t.Fatalf("locals = %v", locals)
	}

	if err := m.Send(info.ID, Command{Cmd: "evaluate", Expression: "a + b", Seq: 7}); err != nil {
		t.Fatal(err)
	}
	ev := rec.next(t, "evaluated")
	if ev["value"] != "3" || ev["seq"] != float64(7) {
		t.Fatalf("evaluated = %v", ev)
	}

	if err := m.Send(info.ID, Command{Cmd: "next"}); err != nil {
		t.Fatal(err)
	}
	stopped = rec.next(t, "stopped")
	if line := stopped["frames"].([]any)[0].(map[string]any)["line"]; line != float64(3) {
		t.Fatalf("after next, line = %v", line)
	}

	if err := m.Send(info.ID, Command{Cmd: "continue"}); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-rec.exit:
		if code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the program did not exit")
	}
	rec.mu.Lock()
	out := rec.output.String()
	rec.mu.Unlock()
	if !strings.Contains(out, "x 3") {
		t.Fatalf("output = %q", out)
	}
}

func TestUncaughtExceptionStops(t *testing.T) {
	needPython(t)
	root := writeProgram(t, "def boom():\n    return 1 / 0\n\nboom()\n")
	m := NewManager()
	defer m.CloseAll()
	rec := newRecorder()
	info, err := m.Start(Options{Root: root, Program: "proj/prog.py", Env: Environment()}, rec.handlers())
	if err != nil {
		t.Fatal(err)
	}
	stopped := rec.next(t, "stopped")
	top := stopped["frames"].([]any)[0].(map[string]any)
	if stopped["reason"] != "exception" || top["line"] != float64(2) || !strings.Contains(stopped["description"].(string), "ZeroDivisionError") {
		t.Fatalf("stopped = %v", stopped)
	}
	if err := m.Send(info.ID, Command{Cmd: "continue"}); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-rec.exit:
		if code != 1 {
			t.Fatalf("exit code = %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the program did not exit")
	}
}

func TestStopKillsTheProgram(t *testing.T) {
	needPython(t)
	root := writeProgram(t, "import time\nwhile True:\n    time.sleep(0.05)\n")
	m := NewManager()
	rec := newRecorder()
	info, err := m.Start(Options{Root: root, Program: "proj/prog.py", Env: Environment()}, rec.handlers())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(info.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rec.exit:
	case <-time.After(10 * time.Second):
		t.Fatal("the program kept running")
	}
}

func TestRefusesPathsOutsideTheRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := resolve(root, "../etc/passwd"); err == nil {
		t.Fatal("a path outside the root was accepted")
	}
	m := NewManager()
	if _, err := m.Start(Options{Root: root, Program: "notes.txt"}, Handlers{}); err == nil {
		t.Fatal("a non-Python file was accepted")
	}
	if rel, ext := relative(root, "/usr/lib/python3/os.py"); !ext || rel != "/usr/lib/python3/os.py" {
		t.Fatalf("relative = %q %v", rel, ext)
	}
}

func TestFindPythonPrefersTheProjectVenv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the venv layout differs on Windows")
	}
	dir := t.TempDir()
	venv := filepath.Join(dir, ".venv", "bin")
	if err := os.MkdirAll(venv, 0o755); err != nil {
		t.Fatal(err)
	}
	py := filepath.Join(venv, "python")
	if err := os.WriteFile(py, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindPython(filepath.Join(dir, "src")); got != py {
		t.Fatalf("FindPython = %q", got)
	}
}

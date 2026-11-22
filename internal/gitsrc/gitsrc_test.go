package gitsrc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseRepo(t *testing.T) {
	good := map[string]Repo{
		"https://github.com/pallets/flask":            {CloneURL: "https://github.com/pallets/flask.git", Owner: "pallets", Name: "flask"},
		"https://github.com/pallets/flask.git":        {CloneURL: "https://github.com/pallets/flask.git", Owner: "pallets", Name: "flask"},
		"https://github.com/pallets/flask/tree/2.0.x": {CloneURL: "https://github.com/pallets/flask.git", Owner: "pallets", Name: "flask", Ref: "2.0.x"},
		"https://gitlab.com/g/p/-/tree/main/src":      {CloneURL: "https://gitlab.com/g/p.git", Owner: "g", Name: "p", Ref: "main"},
		"git@github.com:expressjs/express.git":        {CloneURL: "https://github.com/expressjs/express.git", Owner: "expressjs", Name: "express"},
		"https://github.com/o/r/releases/tag/v1.2.3":  {CloneURL: "https://github.com/o/r.git", Owner: "o", Name: "r", Ref: "v1.2.3"},
		"  https://codeberg.org/forgejo/forgejo\n":    {CloneURL: "https://codeberg.org/forgejo/forgejo.git", Owner: "forgejo", Name: "forgejo"},
	}
	for in, want := range good {
		got, err := ParseRepo(in)
		if err != nil || got != want {
			t.Errorf("ParseRepo(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "flask", "http://github.com/a/b", "ssh://git@github.com/a/b", "file:///etc/passwd",
		"https://localhost/a/b", "https://127.0.0.1/a/b", "https://intranet/a/b", "https://github.com/a",
		"https://user:pw@github.com/a/b", "https://github.com:8443/a/b", "https://github.com/a/b/tree/--upload-pack=x",
		"https://github.com/a/b/tree/../../x", "ext::sh -c id", "https://github.com/a%2fb/c",
	} {
		if _, err := ParseRepo(in); err == nil {
			t.Errorf("ParseRepo(%q) must be refused", in)
		}
	}
}

func TestCloneLocalRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	src := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", src, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	_ = os.WriteFile(filepath.Join(src, "app.py"), []byte("print(1)\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "init")

	dest := filepath.Join(t.TempDir(), "out")
	repo := Repo{CloneURL: "file://" + src, Owner: "o", Name: "r"}

	// The default policy refuses a local transport.
	if err := Clone(context.Background(), repo, dest, Options{}); err == nil {
		t.Fatal("a file:// clone must be refused by default")
	}
	if err := Clone(context.Background(), repo, dest, Options{Protocols: "file"}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "app.py")); string(b) != "print(1)\n" {
		t.Errorf("checkout is missing the file: %q", b)
	}
	if err := Clone(context.Background(), Repo{CloneURL: "file:///definitely/missing", Name: "x"}, filepath.Join(t.TempDir(), "o"), Options{Protocols: "file"}); err == nil {
		t.Error("a missing repository must fail")
	}
}

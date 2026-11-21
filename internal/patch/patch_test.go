package patch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const simpleDiff = `--- a/pkg/mod.py
+++ b/pkg/mod.py
@@ -1,3 +1,3 @@
 import os
-token = request.args["t"]
+token = hmac.compare_digest(request.args["t"], secret)
 return token
`

func TestParseUnifiedDiff(t *testing.T) {
	diffs, err := Parse(simpleDiff)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d file diffs, want 1", len(diffs))
	}
	d := diffs[0]
	// The conventional a/ and b/ prefixes are stripped so the path can be
	// resolved against the workspace root.
	if d.OldPath != "pkg/mod.py" || d.NewPath != "pkg/mod.py" {
		t.Errorf("paths = %q / %q", d.OldPath, d.NewPath)
	}
	if len(d.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(d.Hunks))
	}
	h := d.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 3 {
		t.Errorf("hunk range = %+v", h)
	}
	if len(h.Lines) != 4 {
		t.Errorf("got %d hunk lines, want 4", len(h.Lines))
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"only whitespace":   "   \n\n",
		"hunk without file": "@@ -1,1 +1,1 @@\n-a\n+b\n",
		"bad range":         "--- a/x\n+++ b/x\n@@ bogus @@\n-a\n+b\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(in); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestApplyToContent(t *testing.T) {
	original := "import os\ntoken = request.args[\"t\"]\nreturn token\n"
	diffs, err := Parse(simpleDiff)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := ApplyToContent(original, diffs[0].Hunks)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "import os\ntoken = hmac.compare_digest(request.args[\"t\"], secret)\nreturn token\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestApplyRejectsContextMismatch(t *testing.T) {
	original := "something completely different\n"
	diffs, _ := Parse(simpleDiff)
	if _, err := ApplyToContent(original, diffs[0].Hunks); err == nil {
		t.Error("expected the patch to fail against unrelated content")
	}
}

// TestApplyToContentSurvivesAWrongLineCount is the regression test for a
// crash the fuzz target found: the applier used to take the number of source
// lines a hunk consumes from the count in the hunk header, so a header that
// declares more lines than the file has made it slice past the end of the
// source and panic. The count is now derived from the body of the hunk.
//
// A patch like this is exactly what a language model proposes when it
// miscounts, so the path is reachable from untrusted input.
func TestApplyToContentSurvivesAWrongLineCount(t *testing.T) {
	cases := []struct {
		name string
		diff string
	}{
		{
			name: "declared count larger than the file",
			diff: "--- a/x\n+++ b/x\n@@ -1,999999999 +1,1 @@\n a\n",
		},
		{
			name: "header points past the end of the file, additions only",
			diff: "--- a/x\n+++ b/x\n@@ -999999999,0 +1,1 @@\n+b\n",
		},
		{
			name: "declared count larger than the file, removal",
			diff: "--- a/x\n+++ b/x\n@@ -1,999999999 +1,0 @@\n-a\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diffs, err := Parse(tc.diff)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// The point of the test is that this call returns at all.
			out, err := ApplyToContent("a\n", diffs[0].Hunks)
			if err != nil {
				t.Logf("the patch was rejected, which is fine: %v", err)
				return
			}
			if strings.Contains(out, "999999999") {
				t.Errorf("the output leaked the declared count: %q", out)
			}
		})
	}
}

func TestApplyToFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "mod.py")
	original := "import os\ntoken = request.args[\"t\"]\nreturn token\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Apply(root, "pkg/mod.py", simpleDiff); err != nil {
		t.Fatalf("apply: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hmac.compare_digest") {
		t.Errorf("the patch was not applied: %q", data)
	}
}

func TestApplyRefusesPathEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "evil.py")
	if err := os.WriteFile(outside, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := `--- a/../evil.py
+++ b/../evil.py
@@ -1,1 +1,1 @@
-original
+pwned
`
	err := Apply(root, "../evil.py", diff)
	if err == nil {
		t.Fatal("expected the patch to be refused")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(outside)
	if strings.Contains(string(data), "pwned") {
		t.Error("the file outside the workspace was modified")
	}
}

func TestApplierRecordsChanges(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pkg")
	os.MkdirAll(dir, 0o755)
	target := filepath.Join(dir, "mod.py")
	os.WriteFile(target, []byte("import os\ntoken = request.args[\"t\"]\nreturn token\n"), 0o644)

	a := NewApplier()
	if err := a.Apply(context.Background(), root, "pkg/mod.py", simpleDiff); err != nil {
		t.Fatalf("apply: %v", err)
	}
	changes, err := a.Changes(context.Background(), root)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Path != "pkg/mod.py" {
		t.Errorf("path = %q", changes[0].Path)
	}
}

func TestValidateDoesNotWrite(t *testing.T) {
	content := "import os\ntoken = request.args[\"t\"]\nreturn token\n"
	if err := Validate(content, simpleDiff); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := Validate("totally different\n", simpleDiff); err == nil {
		t.Error("expected validation to fail")
	}
}

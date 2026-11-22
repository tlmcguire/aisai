package patch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedDiffRoundTrips(t *testing.T) {
	cases := [][2]string{
		{"a\nb\nc\n", "a\nB\nc\n"},
		{"a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "b\nc\n"},
		{"", "x\ny\n"},
		{"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n", "1\n2\nX\n4\n5\n6\n7\n8\n9\n10\nY\n12\n"},
	}
	for _, c := range cases {
		diff := UnifiedDiff("f.txt", c[0], c[1])
		if diff == "" {
			t.Fatalf("no diff for %q -> %q", c[0], c[1])
		}
		diffs, err := Parse(diff)
		if err != nil || len(diffs) != 1 {
			t.Fatalf("parse %q: %v", diff, err)
		}
		got, err := ApplyToContent(c[0], diffs[0].Hunks)
		if err != nil {
			t.Fatalf("apply %q: %v\n%s", c, err, diff)
		}
		if strings.TrimSuffix(got, "\n") != strings.TrimSuffix(c[1], "\n") {
			t.Errorf("round trip: got %q want %q\n%s", got, c[1], diff)
		}
	}
	if UnifiedDiff("f", "same\n", "same\n") != "" {
		t.Error("identical content must give an empty diff")
	}
}

func TestWriteRecordsAndReverts(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "pkg", "a.py")
	_ = os.MkdirAll(filepath.Dir(file), 0o755)
	_ = os.WriteFile(file, []byte("x = 1\ny = 2\n"), 0o644)
	a := NewApplier()
	ctx := context.Background()

	changed, err := a.Write(ctx, root, "pkg/a.py", "x = 1\ny = 3\n")
	if err != nil || !changed {
		t.Fatalf("write: changed=%v err=%v", changed, err)
	}
	if got, _ := os.ReadFile(file); string(got) != "x = 1\ny = 3\n" {
		t.Fatalf("content not written: %q", got)
	}
	if changes, _ := a.Changes(ctx, root); len(changes) != 1 {
		t.Fatalf("want 1 recorded change, got %d", len(changes))
	}
	if again, _ := a.Write(ctx, root, "pkg/a.py", "x = 1\ny = 3\n"); again {
		t.Error("an identical write must not record a change")
	}
	if ok, err := a.Revert(ctx, root, "pkg/a.py"); err != nil || !ok {
		t.Fatalf("revert: %v %v", ok, err)
	}
	if got, _ := os.ReadFile(file); string(got) != "x = 1\ny = 2\n" {
		t.Errorf("revert did not restore: %q", got)
	}
	if _, err := a.Write(ctx, root, "../escape.py", "x"); err == nil {
		t.Error("a write outside the workspace must be refused")
	}
}

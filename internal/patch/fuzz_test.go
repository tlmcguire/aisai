package patch

import (
	"strings"
	"testing"
)

// This file holds the fuzz targets for the unified-diff parser.
//
// The parser is on the path from untrusted input to the file system: the
// model proposes a diff, the diff is parsed, and the result is applied to a
// file inside the audit workspace. A parser that panics on a crafted diff
// takes the backend down, so the parser is fuzzed with arbitrary bytes.
//
// Known issue, found by this fuzz target and fixed in internal/patch/patch.go:
// ApplyToContent used to panic with "slice bounds out of range" when a hunk
// header declared more old lines than the target file had, because the applier
// trusted the count in the header instead of counting the lines of the hunk
// body, and then sliced past the end of the source. A second variant of the
// same bug panicked when the header pointed past the end of the file and the
// hunk consisted of additions only. The reproduction was:
//
//	--- a/x
//	+++ b/x
//	@@ -1,999999999 +1,1 @@
//	 a
//
// applied to the content "a\n". The applier now derives the consumed line
// count from the body and refuses a header that points past the end of the
// file. The seed below is kept: it is the regression case, and every panic
// now fails the run.

// fuzzSeeds are the interesting starting points for the parser.
var fuzzSeeds = []string{
	// A valid diff.
	"--- a/pkg/mod.py\n+++ b/pkg/mod.py\n@@ -1,3 +1,3 @@\n import os\n-token = request.args[\"t\"]\n+token = hmac.compare_digest(request.args[\"t\"], secret)\n",
	// A diff that stops in the middle of a hunk.
	"--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n import os\n-token = 1\n",
	// A hunk header that cannot be parsed.
	"--- a/x\n+++ b/x\n@@ bogus @@\n-a\n+b\n",
	// A hunk header with a range that is not a number.
	"--- a/x\n+++ b/x\n@@ -x,1 +y,1 @@\n-a\n+b\n",
	// A hunk header with a missing range.
	"--- a/x\n+++ b/x\n@@ @@\n-a\n+b\n",
	// A hunk outside a file section.
	"@@ -1,1 +1,1 @@\n-a\n+b\n",
	// A file section without hunks.
	"--- a/x\n+++ b/x\n",
	// Two file sections.
	"--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n-a\n+b\n--- a/y\n+++ b/y\n@@ -5,2 +5,2 @@\n-p\n+q\n",
	// A diff with CRLF line endings.
	"--- a/x\r\n+++ b/x\r\n@@ -1,1 +1,1 @@\r\n-a\r\n+b\r\n",
	// A diff with a UTF-8 byte order mark.
	"\ufeff--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n-a\n+b\n",
	// A diff that declares the no-newline marker.
	"--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n\\ No newline at end of file\n-a\n+b\n",
	// An empty diff.
	"",
	// A diff with a very large declared line count. This is the input that
	// used to make ApplyToContent panic; it is the regression case for the
	// fix and is kept as a seed.
	"--- a/x\n+++ b/x\n@@ -1,999999999 +1,1 @@\n a\n",
	// The same input with a context line that matches the source, so the
	// applier really reaches the slicing code.
	"--- a/x\n+++ b/x\n@@ -1,999999999 +1,1 @@\n alpha\n",
	// A diff whose body does not match the declared counts.
	"--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n-a\n+b\n+c\n+d\n",
	// A diff with an empty file header and a headerless hunk. Found by the
	// fuzzer: it deletes lines, which is what showed that the applier
	// assertion has to allow a line of the source to disappear.
	"--- \n@@00,2 0@@",
}

// FuzzUnifiedDiff fuzzes the unified-diff parser with arbitrary bytes.
//
// It asserts three properties:
//
//  1. the parser never panics;
//  2. a parse error comes with no result at all, never a partially parsed
//     diff that a caller might apply;
//  3. a successful parse stays proportional to the input, so a crafted diff
//     cannot make the parser allocate without bound.
func FuzzUnifiedDiff(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		unified := string(data)

		diffs, err := Parse(unified)

		if err != nil {
			// A rejected diff must be rejected completely: a caller that
			// ignores the error must not find a half-parsed diff to apply.
			if diffs != nil {
				t.Fatalf("Parse returned %d file diffs together with the error %v", len(diffs), err)
			}
			return
		}

		// A successful parse is bounded by the size of the input.
		inputLines := strings.Count(unified, "\n") + 1
		if len(diffs) == 0 {
			t.Fatal("Parse succeeded without returning a file section")
		}
		if len(diffs) > inputLines {
			t.Fatalf("Parse returned %d file sections for %d lines of input", len(diffs), inputLines)
		}

		totalHunkLines := 0
		for _, d := range diffs {
			if len(d.Hunks) == 0 {
				t.Fatalf("the file section for %q was accepted without a hunk", d.NewPath)
			}
			if len(d.Hunks) > inputLines {
				t.Fatalf("the file section for %q holds %d hunks for %d lines of input", d.NewPath, len(d.Hunks), inputLines)
			}
			for _, h := range d.Hunks {
				totalHunkLines += len(h.Lines)
				for _, l := range h.Lines {
					if l == "" {
						t.Fatal("the parser produced an empty hunk line")
					}
					switch l[0] {
					case ' ', '-', '+':
					default:
						t.Fatalf("the parser produced the hunk line %q", l)
					}
				}
			}
		}
		if totalHunkLines > inputLines {
			t.Fatalf("the parser produced %d hunk lines for %d lines of input", totalHunkLines, inputLines)
		}

		// The parse is deterministic: the same bytes always give the same
		// diff, so a patch that was reviewed stays the patch that is applied.
		again, err := Parse(unified)
		if err != nil {
			t.Fatalf("the second parse failed: %v", err)
		}
		if len(again) != len(diffs) {
			t.Fatalf("the parser is not deterministic: %d file sections then %d", len(diffs), len(again))
		}
		for i := range diffs {
			if diffs[i].OldPath != again[i].OldPath || diffs[i].NewPath != again[i].NewPath {
				t.Fatalf("the parser is not deterministic for file section %d", i)
			}
			if len(diffs[i].Hunks) != len(again[i].Hunks) {
				t.Fatalf("the parser is not deterministic for the hunks of file section %d", i)
			}
		}
	})
}

// FuzzApplyToContent fuzzes the applier with the hunks of a parsed diff.
//
// The applier is the part that touches file content, so it is fuzzed with the
// hunks a parser produces from arbitrary bytes. Every panic fails the run:
// the crash this target was written to find has been fixed, so nothing is
// exempt any more.
func FuzzApplyToContent(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				panic(r)
			}
		}()

		diffs, err := Parse(string(data))
		if err != nil {
			return
		}
		const original = "alpha\nbeta\ngamma\n"
		for _, d := range diffs {
			// The applier may drop lines of the source and may add the lines
			// of the hunk. It may not invent anything else.
			allowed := map[string]bool{}
			for _, l := range strings.Split(original, "\n") {
				allowed[l] = true
			}
			for _, h := range d.Hunks {
				for _, l := range h.Lines {
					if l[0] == '+' {
						allowed[l[1:]] = true
					}
				}
			}

			// The applier must either produce content or an error. It must
			// never silently produce something that is not a function of the
			// input.
			out, err := ApplyToContent(original, d.Hunks)
			if err != nil {
				if out != "" {
					t.Fatalf("ApplyToContent returned content together with the error %v", err)
				}
				continue
			}
			for _, l := range strings.Split(out, "\n") {
				if !allowed[l] {
					t.Fatalf("ApplyToContent invented the line %q: %q", l, out)
				}
			}
		}
	})
}

// Package patch parses, validates and applies unified diffs inside an audit
// workspace.
//
// The legacy implementation produced remediation as prose, so nothing could
// be reviewed, applied or verified. This package makes a proposed fix a
// real artifact: a diff that either applies cleanly or is rejected with a
// reason.
package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

// Hunk is one contiguous change within a file.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Header   string
	Lines    []string // each prefixed with ' ', '-', '+' or '\'
}

// FileDiff is the set of hunks for one file.
type FileDiff struct {
	OldPath string
	NewPath string
	Hunks   []Hunk
}

// Parse parses a unified diff.
//
// It returns an error when the diff is malformed. A diff with no hunks is
// valid but empty — the caller decides whether that is an error.
func Parse(unified string) ([]FileDiff, error) {
	if strings.TrimSpace(unified) == "" {
		return nil, fmt.Errorf("the patch is empty")
	}
	lines := strings.Split(strings.ReplaceAll(unified, "\r\n", "\n"), "\n")
	// A trailing newline produces an empty final element that is an artefact
	// of the split, not a diff line. Dropping it keeps the last hunk's line
	// count correct.
	if strings.HasSuffix(unified, "\n") && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var (
		files  []FileDiff
		cur    *FileDiff
		hunk   *Hunk
		lineno int
	)
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "--- "):
			// A new file section begins.
			if cur != nil && hunk != nil {
				cur.Hunks = append(cur.Hunks, *hunk)
			}
			if cur != nil {
				files = append(files, *cur)
			}
			cur = &FileDiff{OldPath: stripDiffPrefix(ln[4:])}
			hunk = nil
			lineno = 0

		case strings.HasPrefix(ln, "+++ "):
			if cur == nil {
				return nil, fmt.Errorf("found a +++ header without a preceding --- header")
			}
			cur.NewPath = stripDiffPrefix(ln[4:])

		case strings.HasPrefix(ln, "@@"):
			if cur == nil {
				return nil, fmt.Errorf("found a hunk header outside a file section")
			}
			if hunk != nil {
				cur.Hunks = append(cur.Hunks, *hunk)
			}
			h, err := parseHunkHeader(ln)
			if err != nil {
				return nil, err
			}
			hunk = &h
			lineno = 0

		case strings.HasPrefix(ln, "diff "), strings.HasPrefix(ln, "index "):
			// Git metadata; ignore.

		case hunk != nil:
			if ln == "" {
				// An empty line inside a hunk is an empty context line.
				hunk.Lines = append(hunk.Lines, " ")
				lineno++
				continue
			}
			switch ln[0] {
			case ' ', '-', '+':
				hunk.Lines = append(hunk.Lines, ln)
				lineno++
			case '\\':
				// "\ No newline at end of file" — ignore.
			default:
				// Anything else ends the hunk.
				cur.Hunks = append(cur.Hunks, *hunk)
				hunk = nil
			}
		}
	}
	if cur != nil {
		if hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		files = append(files, *cur)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("the patch contains no file sections")
	}
	for _, f := range files {
		if len(f.Hunks) == 0 {
			return nil, fmt.Errorf("the patch for %q contains no hunks", f.NewPath)
		}
	}
	return files, nil
}

func parseHunkHeader(ln string) (Hunk, error) {
	// The header looks like: @@ -oldStart,oldCount +newStart,newCount @@ heading
	// The closing "@@" is the SECOND occurrence, so search from index 2.
	rel := strings.Index(ln[2:], "@@")
	if rel < 0 {
		return Hunk{}, fmt.Errorf("malformed hunk header: %q", ln)
	}
	end := rel + 2
	body := strings.TrimSpace(ln[2:end])
	parts := strings.Fields(body)
	if len(parts) < 2 {
		return Hunk{}, fmt.Errorf("malformed hunk header: %q", ln)
	}
	oldStart, oldCount, err := parseRange(strings.TrimPrefix(parts[0], "-"))
	if err != nil {
		return Hunk{}, err
	}
	newStart, newCount, err := parseRange(strings.TrimPrefix(parts[1], "+"))
	if err != nil {
		return Hunk{}, err
	}
	heading := ""
	if len(ln) > end+2 {
		heading = strings.TrimSpace(ln[end+2:])
	}
	return Hunk{
		OldStart: oldStart,
		OldLines: oldCount,
		NewStart: newStart,
		NewLines: newCount,
		Header:   heading,
	}, nil
}

func parseRange(s string) (start, count int, err error) {
	if i := strings.Index(s, ","); i >= 0 {
		start, err = strconv.Atoi(s[:i])
		if err != nil {
			return 0, 0, fmt.Errorf("malformed range %q", s)
		}
		count, err = strconv.Atoi(s[i+1:])
		if err != nil {
			return 0, 0, fmt.Errorf("malformed range %q", s)
		}
		return start, count, nil
	}
	start, err = strconv.Atoi(s)
	if err != nil {
		return 0, 0, fmt.Errorf("malformed range %q", s)
	}
	return start, 1, nil
}

func stripDiffPrefix(p string) string {
	p = strings.TrimSpace(p)
	// Strip a trailing timestamp column, which some generators emit.
	if i := strings.IndexAny(p, "\t"); i >= 0 {
		p = strings.TrimSpace(p[:i])
	}
	// Strip the conventional a/ and b/ path prefixes so the path can be
	// resolved against the workspace root.
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		p = p[2:]
	}
	return p
}

// Apply applies a unified diff to the file at relPath inside root.
//
// It refuses to write outside the workspace, refuses a diff that touches a
// different file than relPath, and refuses a diff whose context does not
// match the current file content.
func Apply(root, relPath, unified string) error {
	diffs, err := Parse(unified)
	if err != nil {
		return err
	}
	if len(diffs) != 1 {
		return fmt.Errorf("expected a patch for exactly one file, got %d", len(diffs))
	}

	target := model.RelPath(relPath)
	fd := diffs[0]
	for _, candidate := range []string{fd.NewPath, fd.OldPath} {
		if candidate == "" || candidate == "/dev/null" {
			continue
		}
		if model.RelPath(candidate) == target {
			fd.NewPath = target
			fd.OldPath = target
			break
		}
	}
	if fd.NewPath != target && fd.OldPath != target {
		return fmt.Errorf("the patch targets %q but the request specified %q", fd.NewPath, target)
	}

	abs, err := resolveInRoot(root, target)
	if err != nil {
		return err
	}

	original, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("read %s: %w", target, err)
	}
	patched, err := ApplyToContent(string(original), fd.Hunks)
	if err != nil {
		return err
	}

	info, err := os.Stat(abs)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(abs, []byte(patched), mode); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}

// ApplyToContent applies hunks to the original content.
//
// The algorithm walks the original file line by line, matching each hunk's
// context and removals at the expected offset. A mismatch is an error rather
// than a best-effort application: a patch that does not apply cleanly must
// be shown to the user, never silently half-applied.
func ApplyToContent(original string, hunks []Hunk) (string, error) {
	src := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	// Preserve a trailing newline if the original had one.
	trailingNewline := strings.HasSuffix(original, "\n")
	if trailingNewline && len(src) > 0 && src[len(src)-1] == "" {
		src = src[:len(src)-1]
	}

	var out []string
	pos := 0 // index into src

	for _, h := range hunks {
		// Locate the hunk's start. Trust the header but verify with context.
		start := h.OldStart - 1
		if start < pos {
			start = pos
		}
		if start > len(src) {
			// The header points past the end of the file. That is a
			// malformed patch, not a reason to slice out of bounds.
			return "", fmt.Errorf("the patch refers to line %d, past the end of the file", h.OldStart)
		}
		// Search forward for the hunk's context if the recorded position does
		// not match, tolerating small offsets.
		if !hunkMatches(src, h, start) {
			found := -1
			limit := start + 200
			if limit > len(src) {
				limit = len(src)
			}
			for i := start; i <= limit; i++ {
				if hunkMatches(src, h, i) {
					found = i
					break
				}
			}
			if found < 0 {
				return "", fmt.Errorf("the patch does not apply cleanly at line %d", h.OldStart)
			}
			start = found
		}

		out = append(out, src[pos:start]...)

		for _, l := range h.Lines {
			switch l[0] {
			case ' ':
				out = append(out, l[1:])
			case '-':
				// removed — do not copy
			case '+':
				out = append(out, l[1:])
			}
		}
		// The body of the hunk is authoritative about how many source lines
		// it consumes. The count in the header is a hint: it is never
		// validated against the body, and a patch proposed by a language
		// model can declare a count of 999999999 in a one-line file.
		// Trusting it here would walk past the end of the source and
		// panic.
		pos = start + hunkSourceLines(h)
	}

	out = append(out, src[pos:]...)
	result := strings.Join(out, "\n")
	if trailingNewline && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}

// hunkSourceLines returns the number of source lines a hunk consumes.
//
// A context or removal line consumes one source line; an addition consumes
// none. The count is derived from the body rather than from the hunk header,
// because the header count is never validated and may be arbitrarily wrong.
func hunkSourceLines(h Hunk) int {
	n := 0
	for _, l := range h.Lines {
		switch l[0] {
		case ' ', '-':
			n++
		}
	}
	return n
}

// hunkMatches reports whether the hunk applies cleanly at src index start.
func hunkMatches(src []string, h Hunk, start int) bool {
	oldIdx := start
	for _, l := range h.Lines {
		switch l[0] {
		case ' ':
			if oldIdx >= len(src) || src[oldIdx] != l[1:] {
				return false
			}
			oldIdx++
		case '-':
			if oldIdx >= len(src) || src[oldIdx] != l[1:] {
				return false
			}
			oldIdx++
		case '+':
			// additions consume no source lines
		}
	}
	return true
}

// resolveInRoot resolves rel against root and refuses anything that escapes.
func resolveInRoot(root, rel string) (string, error) {
	clean := model.RelPath(rel)
	if clean == "" {
		return "", fmt.Errorf("empty path")
	}
	abs := filepath.Join(root, filepath.FromSlash(clean))
	rootClean := filepath.Clean(root)
	if abs != rootClean && !strings.HasPrefix(abs, rootClean+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes the audit workspace")
	}
	return abs, nil
}

// ChangeLog is the record of patches applied inside a workspace.
type ChangeLog struct {
	Path    string    `json:"path"`
	Unified string    `json:"unified"`
	Applied time.Time `json:"appliedAt"`
	// Backup is the workspace-relative path of the pre-patch copy of the
	// file. It is what makes a change revertible.
	Backup string `json:"backup,omitempty"`
	// Reverted records that the change was rolled back.
	Reverted bool `json:"reverted,omitempty"`
}

// changeLogName is the file inside the workspace root that records changes.
const changeLogName = ".aisai-changes.json"

// RecordChange appends a change to the workspace change log.
func RecordChange(root string, c ChangeLog) error {
	path := filepath.Join(root, changeLogName)
	existing, _ := LoadChanges(root)
	existing = append(existing, c)
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadChanges reads the workspace change log.
func LoadChanges(root string) ([]ChangeLog, error) {
	path := filepath.Join(root, changeLogName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ChangeLog
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

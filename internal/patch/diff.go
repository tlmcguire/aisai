package patch

import (
	"fmt"
	"strings"
)

// maxDiffCells bounds the table of the line diff. Past it the changed region
// is reported as one block that is deleted and re-added, which is a correct
// diff, just not a minimal one.
const maxDiffCells = 16 << 20

// diffContext is the number of unchanged lines kept around every change.
const diffContext = 3

type diffOp struct {
	kind byte // ' ', '-' or '+'
	text string
}

// UnifiedDiff renders the difference between two versions of a file as a
// unified diff that Apply accepts.
func UnifiedDiff(relPath, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)
	ops := diffLines(oldLines, newLines)

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", relPath, relPath)

	// Group the changes into hunks with diffContext lines around them.
	type span struct{ from, to int } // op indexes, to exclusive
	var spans []span
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := i
		for i < len(ops) && ops[i].kind != ' ' {
			i++
		}
		from, to := start-diffContext, i+diffContext
		if from < 0 {
			from = 0
		}
		if to > len(ops) {
			to = len(ops)
		}
		if n := len(spans); n > 0 && from <= spans[n-1].to {
			spans[n-1].to = to
		} else {
			spans = append(spans, span{from, to})
		}
	}

	// Line numbers of the start of every op.
	oldAt := make([]int, len(ops)+1)
	newAt := make([]int, len(ops)+1)
	o, n := 1, 1
	for i, op := range ops {
		oldAt[i], newAt[i] = o, n
		if op.kind != '+' {
			o++
		}
		if op.kind != '-' {
			n++
		}
	}
	for _, s := range spans {
		oldCount, newCount := 0, 0
		for _, op := range ops[s.from:s.to] {
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
		}
		oldStart, newStart := oldAt[s.from], newAt[s.from]
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, op := range ops[s.from:s.to] {
			b.WriteByte(op.kind)
			b.WriteString(op.text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// diffLines computes a line diff: common prefix and suffix are trimmed, then
// the middle is aligned with a longest-common-subsequence table.
func diffLines(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]

	var ops []diffOp
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	if len(ma)*len(mb) > maxDiffCells {
		for _, l := range ma {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range mb {
			ops = append(ops, diffOp{'+', l})
		}
	} else {
		ops = append(ops, lcsDiff(ma, mb)...)
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	table := make([][]int32, n+1)
	for i := range table {
		table[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

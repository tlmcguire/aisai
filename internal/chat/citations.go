package chat

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
)

// citationPattern matches a file reference with a line number, for example
// "src/auth.py:42" or "auth.py:42-77".
var citationPattern = regexp.MustCompile(`([A-Za-z0-9_][A-Za-z0-9_./\\-]*\.[A-Za-z0-9_]+):(\d+)`)

// extractCitations parses the file:line references out of an answer and
// resolves them against the retrieved chunks.
//
// A citation that matches no retrieved chunk is still reported, with an
// empty symbol, so that the caller can show it as unresolved.
func extractCitations(answer string, chunks []model.Chunk) []Citation {
	if answer == "" {
		return nil
	}
	matches := citationPattern.FindAllStringSubmatch(answer, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []Citation
	for _, m := range matches {
		file := m[1]
		line, err := strconv.Atoi(m[2])
		if err != nil || line <= 0 {
			continue
		}
		file = model.RelPath(file)
		key := file + ":" + strconv.Itoa(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Citation{File: file, Line: line, Symbol: resolveSymbol(file, line, chunks)})
	}
	return out
}

// resolveSymbol finds the symbol path of the chunk that holds the cited line.
func resolveSymbol(file string, line int, chunks []model.Chunk) string {
	for _, c := range chunks {
		if !sameFile(c.File, file) {
			continue
		}
		if c.StartLine <= line && line <= c.EndLine {
			return c.SymbolPath
		}
	}
	return ""
}

// sameFile reports whether a citation path refers to the same file as a
// chunk path. The citation may be relative to a subdirectory that the chunk
// path spells out in full.
func sameFile(chunkFile, cited string) bool {
	if chunkFile == cited {
		return true
	}
	if strings.HasSuffix(chunkFile, "/"+cited) {
		return true
	}
	if strings.HasSuffix(cited, "/"+chunkFile) {
		return true
	}
	return false
}

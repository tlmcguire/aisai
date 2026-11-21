package sast

import (
	"github.com/tlmcguire/aisai/internal/model"
)

// annotateReachability labels each finding with whether its sink is reachable
// from request-derived data.
//
// The engine deliberately does not build a call graph. Without one, a finding
// can only be called reachable when there is positive evidence for it: the
// scope that contains the sink holds a value derived from a request, tainted
// at or before the sink's line.
//
// Everything else is reported as unknown rather than unreachable. A helper
// function with no request parameter of its own may still be called from one
// that has, so calling it unreachable would be a claim the engine cannot
// support. "Unknown" is the honest answer, and it is the answer a reader of
// the report needs to see.
func annotateReachability(sc *scanner, findings []model.Finding) {
	for i := range findings {
		findings[i].Reachability = model.ReachUnknown

		s := sc.scopeAt(findings[i].StartLine)
		if s == nil {
			// The sink is at module level, outside any function.
			continue
		}
		if s.taintedBefore(findings[i].StartLine) {
			findings[i].Reachability = model.ReachReachable
		}
	}
}

// scopeAt returns the innermost scope that contains the given line, or nil
// when the line is not inside any function or class body.
func (sc *scanner) scopeAt(line int) *scope {
	var best *scope
	for _, s := range sc.scopes {
		if s.startLine == 0 || s.endLine == 0 {
			continue
		}
		if line < s.startLine || line > s.endLine {
			continue
		}
		// The innermost scope is the one that starts last.
		if best == nil || s.startLine > best.startLine {
			best = s
		}
	}
	return best
}

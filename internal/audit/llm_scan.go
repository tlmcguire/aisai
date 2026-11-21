package audit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/scan"
)

// ScanLLM runs the LLM detection pipeline over the audit's chunks.
//
// Unlike the SAST scan, this is slow: one model round-trip per chunk for
// detection and one per candidate finding for verification. It is therefore
// bounded by a worker pool and reports progress as it goes.
//
// When relPath is non-empty only that file's chunks are analysed.
func (m *Manager) ScanLLM(ctx context.Context, id, relPath string) ([]model.Finding, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}
	if m.deps.Detector == nil {
		return nil, fmt.Errorf("LLM scanning is disabled")
	}

	a.mu.RLock()
	files := append([]*ast.File(nil), a.files...)
	a.mu.RUnlock()

	chunks := make([]model.Chunk, 0, len(files)*8)
	for _, f := range files {
		for _, c := range f.Chunks {
			if relPath != "" && model.RelPath(c.File) != model.RelPath(relPath) {
				continue
			}
			chunks = append(chunks, c)
		}
	}
	if len(chunks) == 0 {
		return nil, nil
	}

	workers := m.deps.Config.ScanWorkers
	if workers < 1 {
		workers = 1
	}

	var (
		mu          sync.Mutex
		results     []model.Finding
		firstErr    error
		done        int
		quarantined int
	)

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for _, chunk := range chunks {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		wg.Add(1)
		go func(c model.Chunk) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			reqCtx, cancel := context.WithTimeout(ctx, scanTimeout)
			defer cancel()

			found, err := m.deps.Detector.DetectAndVerify(reqCtx, c)

			mu.Lock()
			done++
			switch {
			case err == nil:
			case errors.Is(err, scan.ErrQuarantined):
				// The chunk tried to steer the model and was withheld.
				// That is a successful outcome, not a scan failure: the
				// rest of the file still gets analysed.
				quarantined++
			case firstErr == nil:
				firstErr = err
			}
			results = append(results, found...)
			percent := float64(done) / float64(len(chunks))
			mu.Unlock()

			for _, f := range found {
				m.emit(model.Event{Type: model.EventFinding, AuditID: id, Finding: &f})
			}
			m.emit(model.Event{
				Type:    model.EventScanProgress,
				AuditID: id,
				Phase:   "llm",
				Percent: percent,
				Message: fmt.Sprintf("Scanned %d/%d chunks (%s)", done, len(chunks), c.File),
			})
		}(chunk)
	}
	wg.Wait()

	results = dedupeFindings(results)
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].File != results[j].File {
			return results[i].File < results[j].File
		}
		if results[i].StartLine != results[j].StartLine {
			return results[i].StartLine < results[j].StartLine
		}
		return results[i].Rule < results[j].Rule
	})

	if _, err := m.deps.Store.Upsert(ctx, id, results); err != nil {
		return results, err
	}
	if err := m.deps.Store.RecordScan(ctx, id, fmt.Sprintf("llm-%d", time.Now().UnixNano()), "llm", len(results), time.Now(), time.Now()); err != nil {
		m.emit(model.Event{Type: model.EventAuditError, AuditID: id, Error: err.Error()})
	}

	message := fmt.Sprintf("LLM scan complete: %d findings", len(results))
	if quarantined > 0 {
		message += fmt.Sprintf(" (%d chunk(s) quarantined: they tried to steer the model)", quarantined)
	}
	m.emit(model.Event{
		Type:    model.EventScanProgress,
		AuditID: id,
		Phase:   "llm",
		Percent: 1,
		Message: message,
	})

	if len(results) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// scanTimeout bounds a single chunk's detection plus verification.
const scanTimeout = 5 * time.Minute

// dedupeFindings removes findings that describe the same issue at the same
// location, keeping the one with the highest confidence.
//
// The SAST engine and the LLM can both flag the same construct. Without this
// the user sees every issue twice.
func dedupeFindings(fs []model.Finding) []model.Finding {
	type key struct {
		file string
		line int
		cwe  string
		rule string
	}
	best := map[key]model.Finding{}
	order := []key{}

	for _, f := range fs {
		k := key{file: model.RelPath(f.File), line: f.StartLine, cwe: f.CWE, rule: f.Rule}
		if prev, ok := best[k]; ok {
			if f.Confidence > prev.Confidence {
				best[k] = f
			}
			continue
		}
		best[k] = f
		order = append(order, k)
	}

	out := make([]model.Finding, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

// ChunksForFile returns the chunks of one file, for the code view.
func (m *Manager) ChunksForFile(id, relPath string) ([]model.Chunk, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}
	abs, err := ResolvePath(a.Root, relPath)
	if err != nil {
		return nil, err
	}
	f, err := ast.Parse(abs)
	if err != nil && len(f.Chunks) == 0 {
		return nil, findings.ErrNotFound
	}
	return f.Chunks, nil
}

// AuditRoot exposes the workspace root of an audit.
func (m *Manager) AuditRoot(id string) (string, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return "", findings.ErrNotFound
	}
	return a.Root, nil
}

// String renders a short description of a package, for logs.
func describePackage(p model.Package) string {
	return strings.TrimSpace(p.Name + " " + p.Version)
}

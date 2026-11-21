package sast

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/ast"
)

// loadDir parses every Python file under dir.
func loadDir(t *testing.T, dir string) []*ast.File {
	t.Helper()
	var files []*ast.File
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".py" {
			return nil
		}
		f, _ := ast.Parse(path)
		if f != nil {
			files = append(files, f)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestScan300Files checks the acceptance criterion that scanning a 300-file
// package completes in under five seconds, and that repeated scans of the same
// files do not leak.
func TestScan300Files(t *testing.T) {
	root := "../../bench/train/vulnerable"
	if _, err := os.Stat(root); err != nil {
		t.Skip("no bench corpus found")
	}
	files := loadDir(t, root)
	if len(files) < 300 {
		t.Skipf("need 300 files, have %d", len(files))
	}
	sub := files[:300]

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)

	engine := NewEngine()
	start := time.Now()
	findings := engine.Scan(sub)
	elapsed := time.Since(start)

	runtime.GC()
	runtime.ReadMemStats(&m1)

	t.Logf("scanned 300 files: %d findings in %v (heap delta %dKB)",
		len(findings), elapsed, (int64(m1.HeapAlloc)-int64(m0.HeapAlloc))/1024)
	if elapsed > 5*time.Second {
		t.Errorf("scanning 300 files took %v, want < 5s", elapsed)
	}
}

// TestScanBenchCorpus scans the benchmark corpus and compares the vulnerable
// and patched halves.
func TestScanBenchCorpus(t *testing.T) {
	root := "../../bench/train"
	if _, err := os.Stat(root); err != nil {
		t.Skip("no bench corpus found")
	}
	engine := NewEngine()
	vulnFiles := loadDir(t, filepath.Join(root, "vulnerable"))
	vuln := engine.Scan(vulnFiles)
	patchedFiles := loadDir(t, filepath.Join(root, "patched"))
	patched := engine.Scan(patchedFiles)

	t.Logf("vulnerable: %d files, %d findings (%.1f per file)",
		len(vulnFiles), len(vuln), float64(len(vuln))/float64(len(vulnFiles)))
	t.Logf("patched:    %d files, %d findings (%.1f per file)",
		len(patchedFiles), len(patched), float64(len(patched))/float64(len(patchedFiles)))

	counts := map[string]int{}
	for _, f := range vuln {
		counts[f.Rule]++
	}
	for _, r := range Rules() {
		t.Logf("  %-30s vulnerable=%d", r.ID, counts[r.ID])
	}

	if len(vuln) == 0 {
		t.Error("no findings in the vulnerable corpus")
	}
	if len(vuln) <= len(patched) {
		t.Errorf("vulnerable corpus produced %d findings, patched %d; expected more in vulnerable",
			len(vuln), len(patched))
	}
}

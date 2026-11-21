package external

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// BenchmarkPythonName is the corpus name used in the reports.
const BenchmarkPythonName = "benchmarkpython"

// BenchmarkPythonURL is the pinned URL of the OWASP BenchmarkPython archive.
//
// The URL addresses one immutable commit of the benchmark repository, so the
// bytes it yields never change under a running benchmark. The commit was
// resolved on 2026-11-21.
//
// The benchmark is licensed under GPL-3.0. That is the reason the corpus is
// not vendored into this repository and not shipped in any binary of this
// project: it is fetched at benchmark time, into a cache directory that is
// not part of the repository.
const BenchmarkPythonURL = "https://codeload.github.com/OWASP-Benchmark/BenchmarkPython/tar.gz/f1291485808b66e20ddb6b01b10dc71b3df8c8ba"

// BenchmarkPythonArchive is the file name of the benchmark archive in the
// cache directory.
const BenchmarkPythonArchive = "benchmarkpython.tar.gz"

// The expected verdicts of a benchmark case.
const (
	// BenchmarkTP is a line the case expects to be reported.
	BenchmarkTP = "TP"
	// BenchmarkFP is a line the case expects not to be reported.
	BenchmarkFP = "FP"
)

// BenchmarkCase is one test case of the benchmark.
//
// A test case is a whole Python file, not a snippet, and the expectations are
// per line: the lines the case expects to be reported (true positives) and
// the lines it expects to stay silent on (false positives).
type BenchmarkCase struct {
	// Name is the identifier of the case, "BenchmarkTest00001".
	Name string
	// Category is the weakness family of the case, "pathtraver".
	Category string
	// Path is the repository-relative path of the test file.
	Path string
	// Vulnerable reports whether the case holds a real vulnerability.
	Vulnerable bool
	// CWE is the CWE identifier the case expects, "CWE-22".
	CWE string
	// Expected maps a line number to the verdict expected on it: either
	// BenchmarkTP or BenchmarkFP.
	//
	// It is empty for the file-level layout of the expected results, where
	// the verdict is a property of the whole file.
	Expected map[int]string
}

// HasLineExpectations reports whether the case carries per-line verdicts.
func (c BenchmarkCase) HasLineExpectations() bool { return len(c.Expected) > 0 }

// ParseBenchmarkExpectedResults parses an OWASP BenchmarkPython expected
// results file.
//
// Two layouts are supported.
//
// The per-line layout has the columns
// "test name, category, real vulnerability, cwe, filename, line, issue",
// where the issue is "TP" or "FP" and the line is the line of the test file
// the verdict applies to.
//
// The file-level layout has the columns "test name, category, real
// vulnerability, cwe". It carries no line, so the verdict applies to the
// whole test file.
//
// The layout is detected from the number of columns, so the same parser
// reads both. Comment lines, which the benchmark writes as a header, are
// skipped.
func ParseBenchmarkExpectedResults(r io.Reader) ([]BenchmarkCase, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	cases := map[string]*BenchmarkCase{}
	var order []string

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("external: parse the expected results: %w", err)
		}
		if len(rec) == 0 {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(rec[0]), "#") {
			continue
		}
		name := strings.TrimSpace(rec[0])
		if name == "" {
			continue
		}
		if len(rec) < 4 {
			return nil, fmt.Errorf(
				"external: the expected results row %q has %d columns, want at least 4", name, len(rec))
		}

		c, ok := cases[name]
		if !ok {
			c = &BenchmarkCase{
				Name:       name,
				Category:   strings.TrimSpace(rec[1]),
				Vulnerable: strings.EqualFold(strings.TrimSpace(rec[2]), "true"),
				CWE:        NormalizeCWE(strings.TrimSpace(rec[3])),
				Expected:   map[int]string{},
			}
			cases[name] = c
			order = append(order, name)
		}
		if c.Path == "" {
			c.Path = benchmarkCasePath(name, rec)
		}

		if len(rec) >= 7 {
			line, err := strconv.Atoi(strings.TrimSpace(rec[5]))
			if err != nil {
				return nil, fmt.Errorf(
					"external: the expected results row %q has the line %q, which is not a number",
					name, rec[5])
			}
			issue := strings.ToUpper(strings.TrimSpace(rec[6]))
			if issue != BenchmarkTP && issue != BenchmarkFP {
				return nil, fmt.Errorf(
					"external: the expected results row %q has the issue %q, want TP or FP", name, rec[6])
			}
			c.Expected[line] = issue
		}
	}

	out := make([]BenchmarkCase, 0, len(order))
	for _, name := range order {
		out = append(out, *cases[name])
	}
	return out, nil
}

// benchmarkCasePath returns the path of the test file of a case.
//
// The per-line layout names the file in its fifth column; the file-level
// layout does not, and the benchmark keeps the test files in "testcode".
func benchmarkCasePath(name string, rec []string) string {
	if len(rec) >= 5 {
		file := strings.TrimSpace(rec[4])
		if file != "" {
			file = NormalizePath(file)
			if !strings.Contains(file, "/") {
				file = "testcode/" + file
			}
			return file
		}
	}
	return "testcode/" + name + ".py"
}

// BenchmarkMatch is the classification of one finding against one case.
type BenchmarkMatch struct {
	// Classification is "TP", "FP" or "FN".
	Classification string
	// CaseName is the name of the case the finding was matched against.
	CaseName string
	// Line is the line the finding was reported on.
	Line int
	// Finding is the finding that was classified.
	Finding Finding
	// HasFinding reports whether the finding is set.
	HasFinding bool
}

// BenchmarkResult is the outcome of matching findings against the cases.
type BenchmarkResult struct {
	// Counts is the confusion matrix.
	Counts Counts
	// Matches holds one entry per finding and per missed expectation.
	Matches []BenchmarkMatch
	// Findings is the number of findings that were classified.
	Findings int
}

// MatchBenchmark classifies findings against the benchmark cases.
//
// The semantics follow the benchmark's own scoring model.
//
// A finding on a test file is a true positive when the case expects a
// vulnerability there and the CWE of the finding is the one the case
// expects. It is a false positive when the case expects no vulnerability,
// or when the CWE of the finding is another weakness, or when the finding
// falls on a line the case expects to stay silent on.
//
// With the per-line layout the verdict of a finding is decided by the line
// it was reported on: a line the case expects to be reported is a true
// positive, a line it expects to stay silent on is a false positive, and a
// line it says nothing about is a false positive as well — the benchmark
// expects the tool to be quiet there.
//
// Without the per-line layout the verdict is a property of the whole file:
// the file is expected to hold a vulnerability, or not.
//
// A CWE the finding does not report is accepted, because the benchmark's
// expected results do not always carry one: the CWE gate only applies when
// both sides declare a CWE.
func MatchBenchmark(findings []Finding, cases []BenchmarkCase) BenchmarkResult {
	res := BenchmarkResult{}

	byPath := map[string]*BenchmarkCase{}
	for i := range cases {
		byPath[NormalizePath(cases[i].Path)] = &cases[i]
	}

	// The expectations that were met, per case, so that a case with
	// several expected lines is not counted as missed twice.
	met := map[string]map[int]bool{}
	// The lines that carried a finding, whatever its verdict.
	seen := map[string]map[int]bool{}
	// The cases that were hit by a finding that passed the gate.
	hit := map[string]bool{}

	for _, f := range findings {
		res.Findings++
		c, ok := byPath[NormalizePath(f.Path)]
		if !ok {
			res.Counts.FP++
			res.Matches = append(res.Matches, BenchmarkMatch{
				Classification: MatchFP,
				Line:           f.Line,
				Finding:        f,
				HasFinding:     true,
			})
			continue
		}

		verdict, ok := benchmarkVerdict(f, *c)
		if !ok {
			res.Counts.FP++
			res.Matches = append(res.Matches, BenchmarkMatch{
				Classification: MatchFP,
				CaseName:       c.Name,
				Line:           f.Line,
				Finding:        f,
				HasFinding:     true,
			})
			continue
		}

		if met[c.Name] == nil {
			met[c.Name] = map[int]bool{}
		}
		if seen[c.Name] == nil {
			seen[c.Name] = map[int]bool{}
		}
		seen[c.Name][f.Line] = true

		switch verdict {
		case BenchmarkTP:
			// Only a finding that met the expectation counts as having
			// met it: a false positive on the same line leaves the
			// expectation missed.
			met[c.Name][f.Line] = true
			hit[c.Name] = true
			res.Counts.TP++
			res.Matches = append(res.Matches, BenchmarkMatch{
				Classification: MatchTP,
				CaseName:       c.Name,
				Line:           f.Line,
				Finding:        f,
				HasFinding:     true,
			})
		default:
			res.Counts.FP++
			res.Matches = append(res.Matches, BenchmarkMatch{
				Classification: MatchFP,
				CaseName:       c.Name,
				Line:           f.Line,
				Finding:        f,
				HasFinding:     true,
			})
		}
	}

	// The expectations that no finding met.
	for _, c := range cases {
		if c.HasLineExpectations() {
			for line, verdict := range c.Expected {
				if verdict == BenchmarkTP {
					if met[c.Name][line] {
						continue
					}
					res.Counts.FN++
					res.Matches = append(res.Matches, BenchmarkMatch{
						Classification: MatchFN,
						CaseName:       c.Name,
						Line:           line,
					})
					continue
				}
				// A line the case expects to stay silent on: a
				// finding on it is a false positive, and silence is
				// a true negative.
				if seen[c.Name][line] {
					continue
				}
				res.Counts.TN++
			}
			continue
		}
		// The file-level layout: the verdict is a property of the whole
		// file. A case that holds a real vulnerability and was not
		// reported is a false negative; a case that holds none and was
		// not reported is a true negative.
		if hit[c.Name] {
			continue
		}
		if c.Vulnerable {
			res.Counts.FN++
			res.Matches = append(res.Matches, BenchmarkMatch{
				Classification: MatchFN,
				CaseName:       c.Name,
			})
			continue
		}
		res.Counts.TN++
	}

	return res
}

// benchmarkVerdict returns the verdict of a finding on a case.
//
// The second result reports whether the finding belongs to the case at all.
func benchmarkVerdict(f Finding, c BenchmarkCase) (string, bool) {
	if c.CWE != "" {
		detected := NormalizeCWE(f.CWE)
		if detected != "" && detected != c.CWE {
			return BenchmarkFP, true
		}
	}
	if !c.HasLineExpectations() {
		if c.Vulnerable {
			return BenchmarkTP, true
		}
		return BenchmarkFP, true
	}
	verdict, ok := c.Expected[f.Line]
	if !ok {
		// The benchmark expects the tool to be silent on the lines it
		// does not name.
		return BenchmarkFP, true
	}
	return verdict, true
}

// BenchmarkPythonCorpus is a fetched OWASP BenchmarkPython corpus.
type BenchmarkPythonCorpus struct {
	// Digest is the SHA-256 of the archive the corpus was read from.
	Digest string
	// Cases holds the test cases of the corpus.
	Cases []BenchmarkCase
	// SourceDir is the directory the test files were extracted into.
	SourceDir string
}

// Files returns the test files of the corpus, keyed by their
// repository-relative path.
//
// Only the files of the test cases are returned. The archive holds other
// Python files — the helpers of the benchmark application — and a finding in
// one of them belongs to no case and would be counted as a false positive
// against nothing.
func (c *BenchmarkPythonCorpus) Files() (map[string]string, error) {
	wanted := map[string]bool{}
	for _, kase := range c.Cases {
		wanted[NormalizePath(kase.Path)] = true
	}

	out := map[string]string{}
	root, err := findArchiveRoot(c.SourceDir)
	if err != nil {
		root = c.SourceDir
	}
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if !wanted[NormalizePath(rel)] {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[NormalizePath(rel)] = string(data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("external: read the test files: %w", err)
	}
	return out, nil
}

// FetchBenchmarkPython downloads the OWASP BenchmarkPython corpus into the
// cache directory.
//
// The expected results are read from the file the benchmark's scoring
// configuration names; the file-level layout of the current release is
// supported as well as the per-line layout of the earlier ones.
func FetchBenchmarkPython(ctx context.Context, cacheDir string, offline bool) (*BenchmarkPythonCorpus, error) {
	if cacheDir == "" {
		cacheDir = DefaultCacheDir
	}
	bpCache := filepath.Join(cacheDir, BenchmarkPythonName)

	archive, err := Fetch(ctx, bpCache, Artefact{
		Name: BenchmarkPythonArchive,
		URL:  BenchmarkPythonURL,
	}, offline)
	if err != nil {
		return nil, err
	}

	extractDir := filepath.Join(bpCache, "corpus")
	if _, err := os.Stat(extractDir); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("external: stat %s: %w", extractDir, err)
		}
		if err := ExtractTarGz(archive.Path, extractDir); err != nil {
			return nil, err
		}
	}

	root, err := findArchiveRoot(extractDir)
	if err != nil {
		return nil, err
	}

	expectedPath, err := findExpectedResults(root)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(expectedPath)
	if err != nil {
		return nil, fmt.Errorf("external: open %s: %w", expectedPath, err)
	}
	defer f.Close()
	cases, err := ParseBenchmarkExpectedResults(f)
	if err != nil {
		return nil, err
	}
	for i := range cases {
		if cases[i].Path == "" {
			cases[i].Path = "testcode/" + cases[i].Name + ".py"
		}
	}

	return &BenchmarkPythonCorpus{
		Digest:    archive.SHA256,
		Cases:     cases,
		SourceDir: extractDir,
	}, nil
}

// findExpectedResults returns the path of the expected results file of an
// extracted archive.
//
// The scoring configuration names the file; the file is used when the
// configuration is missing.
func findExpectedResults(root string) (string, error) {
	configPath := filepath.Join(root, "scoringconfigfile.yaml")
	if data, err := os.ReadFile(configPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "expectedresults:") {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(line, "expectedresults:"))
			name = strings.Trim(name, "\"'")
			if name != "" {
				candidate := filepath.Join(root, name)
				if _, err := os.Stat(candidate); err == nil {
					return candidate, nil
				}
			}
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, "expectedresults*.csv"))
	if err != nil {
		return "", fmt.Errorf("external: look for the expected results: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("external: %s holds no expected results file", root)
	}
	sort.Strings(matches)
	return matches[0], nil
}

// BenchmarkPythonRun is the result of running one detector over the
// BenchmarkPython corpus.
type BenchmarkPythonRun struct {
	// Detector is the name of the engine.
	Detector string
	// Files is the number of test files that were scanned.
	Files int
	// Findings is the number of findings the engine reported.
	Findings int
	// Counts is the confusion matrix of the run.
	Counts Counts
	// Matches holds one entry per finding and per missed expectation.
	Matches []BenchmarkMatch
	// Errors holds the files that could not be scanned, with the reason.
	Errors []string
}

// RunBenchmarkPython scans the corpus with a detector and matches the
// findings.
//
// The limit caps the number of test files that are scanned; zero scans every
// file.
func RunBenchmarkPython(ctx context.Context, corpus *BenchmarkPythonCorpus, det Detector, limit int) (*BenchmarkPythonRun, error) {
	run := &BenchmarkPythonRun{Detector: det.Name()}

	files, err := corpus.Files()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var findings []Finding
	for _, p := range paths {
		if limit > 0 && run.Files >= limit {
			break
		}
		run.Files++
		found, err := det.Detect(ctx, p, files[p])
		if err != nil {
			run.Errors = append(run.Errors, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		findings = append(findings, found...)
	}

	run.Findings = len(findings)
	res := MatchBenchmark(findings, corpus.Cases)
	run.Counts = res.Counts
	run.Matches = res.Matches
	return run, nil
}

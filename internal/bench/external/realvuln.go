package external

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
)

// RealVulnLineTolerance is the number of lines a reported line may differ
// from the ground-truth line and still match.
//
// This is the tolerance of the RealVuln scorer (scorer/matcher.py,
// DEFAULT_LINE_TOLERANCE). The ground truth locates a vulnerability by a line
// range; a tool that reports the sink of a flow instead of the injection
// point is right about the vulnerability and wrong about the line, and the
// tolerance is what keeps the measurement about the vulnerability.
const RealVulnLineTolerance = 10

// RealVulnBenchmarkURL is the pinned URL of the RealVuln benchmark archive.
//
// The URL addresses one immutable commit of the benchmark repository, so the
// bytes it yields never change under a running benchmark. The commit was
// resolved on 2026-11-21.
//
// The licence of the benchmark repository is Apache-2.0; the licences of the
// individual target repositories are the licences of those repositories. The
// corpus is not vendored into this repository for that reason, and because it
// is a few hundred megabytes.
const RealVulnBenchmarkURL = "https://codeload.github.com/kolega-ai/Real-Vuln-Benchmark/tar.gz/015244597a86d107f86f9574f9eda6d20928f156"

// RealVulnBenchmarkArchive is the file name of the benchmark archive in the
// cache directory.
const RealVulnBenchmarkArchive = "realvuln-benchmark.tar.gz"

// RealVulnBenchmarkName is the corpus name used in the reports.
const RealVulnBenchmarkName = "realvuln"

// RealVulnLocation is an alternative location of a ground-truth entry.
//
// The benchmark records, for some entries, other places in the repository
// where the same weakness appears. A finding on one of them matches the
// entry, with the same file and line semantics.
type RealVulnLocation struct {
	// File is the repository-relative path of the location.
	File string
	// StartLine is the first line of the location.
	StartLine int
	// EndLine is the last line of the location.
	EndLine int
}

// RealVulnEntry is one ground-truth entry of the RealVuln corpus.
//
// An entry with IsVulnerable false is a false-positive trap: a location that
// looks like a vulnerability but is not one. Reporting on a trap is a false
// positive.
type RealVulnEntry struct {
	// ID is the identifier of the entry.
	ID string
	// IsVulnerable reports whether the location holds a real vulnerability.
	IsVulnerable bool
	// File is the repository-relative path of the location.
	File string
	// AcceptableCWEs are the CWE identifiers a finding may report to
	// match this entry.
	AcceptableCWEs []string
	// StartLine is the first line of the location.
	StartLine int
	// EndLine is the last line of the location.
	EndLine int
	// NonScoring marks an entry whose status cannot be settled from the
	// source alone. Such an entry takes no part in the scoring: reporting
	// on it is not a false positive and missing it is not a false
	// negative.
	NonScoring bool
	// AcceptableLocations are alternative locations of the same weakness.
	AcceptableLocations []RealVulnLocation
}

// RealVulnGroundTruth is a parsed RealVuln ground-truth document.
type RealVulnGroundTruth struct {
	// RepoID is the identifier of the repository.
	RepoID string
	// RepoURL is the location the repository was cloned from.
	RepoURL string
	// CommitSHA is the commit the ground truth was written against.
	CommitSHA string
	// Language is the main language of the repository.
	Language string
	// Entries are the ground-truth entries of the repository.
	Entries []RealVulnEntry
}

// realVulnGroundTruthFile is the JSON layout of a ground-truth document.
type realVulnGroundTruthFile struct {
	SchemaVersion string `json:"schema_version"`
	RepoID        string `json:"repo_id"`
	RepoURL       string `json:"repo_url"`
	CommitSHA     string `json:"commit_sha"`
	Language      string `json:"language"`
	Findings      []struct {
		ID           string   `json:"id"`
		IsVulnerable bool     `json:"is_vulnerable"`
		PrimaryCWE   string   `json:"primary_cwe"`
		Acceptable   []string `json:"acceptable_cwes"`
		File         string   `json:"file"`
		Location     struct {
			StartLine int `json:"start_line"`
			EndLine   int `json:"end_line"`
		} `json:"location"`
		Scoring             string             `json:"scoring"`
		AcceptableLocations []realVulnLocation `json:"acceptable_locations"`
	} `json:"findings"`
}

// realVulnLocation is the JSON layout of an alternative location.
type realVulnLocation struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// ParseRealVulnGroundTruth parses a RealVuln ground-truth document.
//
// The paths of the entries are normalised the way the benchmark scorer
// normalises them, so that a finding and an entry can be compared directly.
func ParseRealVulnGroundTruth(r io.Reader) (*RealVulnGroundTruth, error) {
	var doc realVulnGroundTruthFile
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("external: parse the RealVuln ground truth: %w", err)
	}
	if doc.RepoID == "" {
		return nil, fmt.Errorf("external: the RealVuln ground truth has no repo_id")
	}
	gt := &RealVulnGroundTruth{
		RepoID:    doc.RepoID,
		RepoURL:   doc.RepoURL,
		CommitSHA: doc.CommitSHA,
		Language:  doc.Language,
	}
	for _, f := range doc.Findings {
		e := RealVulnEntry{
			ID:             f.ID,
			IsVulnerable:   f.IsVulnerable,
			File:           NormalizePath(f.File),
			AcceptableCWEs: normalizeCWESet(f.Acceptable),
			StartLine:      f.Location.StartLine,
			EndLine:        f.Location.EndLine,
			NonScoring:     strings.EqualFold(f.Scoring, "non_scoring"),
		}
		if e.ID == "" {
			e.ID = fmt.Sprintf("%s-%03d", doc.RepoID, len(gt.Entries)+1)
		}
		for _, loc := range f.AcceptableLocations {
			e.AcceptableLocations = append(e.AcceptableLocations, RealVulnLocation{
				File:      NormalizePath(loc.File),
				StartLine: loc.StartLine,
				EndLine:   loc.EndLine,
			})
		}
		gt.Entries = append(gt.Entries, e)
	}
	return gt, nil
}

// normalizeCWESet normalises a set of CWE identifiers.
func normalizeCWESet(cwes []string) []string {
	out := make([]string, 0, len(cwes))
	for _, c := range cwes {
		n := NormalizeCWE(c)
		if n == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// RealVulnMatchesEntry reports whether a finding matches a ground-truth
// entry.
//
// The semantics are the ones of the benchmark scorer:
//
//  1. the CWE of the finding must be in the acceptable_cwes set of the
//     entry;
//  2. the file path of the finding must equal the path of the entry, or the
//     path of one of its acceptable locations;
//  3. the reported line must lie within the line range of the location,
//     widened by RealVulnLineTolerance lines on each side.
//
// A finding that reports no line is not penalised on the line test: it can
// still match an entry of the same file, which is what the scorer does.
func RealVulnMatchesEntry(f Finding, e RealVulnEntry) bool {
	if !cweMatches(f.CWE, e.AcceptableCWEs) {
		return false
	}
	if realVulnLocationMatches(f, e.File, e.StartLine, e.EndLine) {
		return true
	}
	for _, loc := range e.AcceptableLocations {
		if realVulnLocationMatches(f, loc.File, loc.StartLine, loc.EndLine) {
			return true
		}
	}
	return false
}

// realVulnLocationMatches reports whether a finding falls on a location.
//
// The line window is [start - tolerance, end + tolerance]. When the location
// has no end line, the window is [start - tolerance, start + tolerance].
func realVulnLocationMatches(f Finding, file string, startLine, endLine int) bool {
	if NormalizePath(f.Path) != NormalizePath(file) {
		return false
	}
	if f.Line <= 0 || startLine <= 0 {
		// The scorer does not penalise a finding that carries no line, and
		// it cannot compare a location that carries none.
		return true
	}
	if endLine <= 0 {
		endLine = startLine
	}
	low := startLine - RealVulnLineTolerance
	high := endLine + RealVulnLineTolerance
	return f.Line >= low && f.Line <= high
}

// cweMatches reports whether a CWE is in an acceptable set.
func cweMatches(cwe string, acceptable []string) bool {
	c := NormalizeCWE(cwe)
	if c == "" {
		return false
	}
	for _, a := range acceptable {
		if NormalizeCWE(a) == c {
			return true
		}
	}
	return false
}

// RealVulnMatch is the classification of one finding or one ground-truth
// entry.
type RealVulnMatch struct {
	// Classification is "TP", "FP", "FN", "TN" or "NS".
	//
	// "NS" is a non-scoring result: a finding on a location whose status
	// the benchmark leaves unsettled, or a non-scoring entry itself. It
	// counts towards no metric.
	Classification string
	// GroundTruthID is the identifier of the ground-truth entry, empty
	// for a finding that matched nothing.
	GroundTruthID string
	// Finding is the finding that was classified. It is the zero value
	// for an entry that was not reported.
	Finding Finding
	// HasFinding reports whether the finding is set.
	HasFinding bool
}

// The classifications a match can carry.
const (
	// MatchTP is a true positive.
	MatchTP = "TP"
	// MatchFP is a false positive.
	MatchFP = "FP"
	// MatchFN is a false negative.
	MatchFN = "FN"
	// MatchTN is a true negative.
	MatchTN = "TN"
	// MatchNS is a non-scoring result.
	MatchNS = "NS"
)

// RealVulnTrapCounts reports the outcome on the false-positive traps.
type RealVulnTrapCounts struct {
	// Traps is the number of false-positive traps in the ground truth.
	Traps int
	// TrapsHit is the number of traps the engine reported on. Every hit
	// is a false positive.
	TrapsHit int
	// TrapsSilent is the number of traps the engine did not report on.
	TrapsSilent int
}

// RealVulnResult is the outcome of matching findings against a ground truth.
type RealVulnResult struct {
	// Counts is the confusion matrix.
	Counts Counts
	// Traps reports the outcome on the false-positive traps.
	Traps RealVulnTrapCounts
	// Matches holds one entry per finding and per unmatched ground-truth
	// entry, in that order.
	Matches []RealVulnMatch
}

// MatchRealVuln classifies findings against the ground-truth entries.
//
// The algorithm is the one of the benchmark scorer:
//
//  1. The entries are partitioned into scored entries and non-scoring
//     entries. The non-scoring ones take no part in the matching: a
//     label-blind assignment could let one take a finding away from a
//     co-located vulnerable entry and silently turn a true positive into a
//     false negative.
//  2. Every finding is matched against the scored entries that are still
//     unmatched and that satisfy the file, CWE and line semantics. When
//     several match, the vulnerable one wins, so that a tool is not
//     penalised by a trap that sits next to a real vulnerability.
//  3. A finding that matched nothing scored is checked against the
//     non-scoring entries on the file and the line only, with no CWE gate:
//     the status of such a location is unsettled whatever CWE the tool
//     chose. A finding that carries no line is never withheld this way.
//  4. A finding that matched nothing at all is a false positive.
//  5. A scored entry that no finding matched is a false negative when it
//     holds a real vulnerability and a true negative when it is a trap.
func MatchRealVuln(findings []Finding, entries []RealVulnEntry) RealVulnResult {
	var scored, nonScoring []RealVulnEntry
	for _, e := range entries {
		if e.NonScoring {
			nonScoring = append(nonScoring, e)
			continue
		}
		scored = append(scored, e)
	}

	matched := make([]bool, len(scored))
	res := RealVulnResult{}

	for _, f := range findings {
		var best int = -1
		for i, e := range scored {
			if matched[i] {
				continue
			}
			if !RealVulnMatchesEntry(f, e) {
				continue
			}
			if best < 0 {
				best = i
				continue
			}
			// A vulnerable entry wins over a trap.
			if e.IsVulnerable && !scored[best].IsVulnerable {
				best = i
			}
		}
		if best >= 0 {
			matched[best] = true
			e := scored[best]
			class := MatchFP
			if e.IsVulnerable {
				class = MatchTP
			}
			switch class {
			case MatchTP:
				res.Counts.TP++
			case MatchFP:
				res.Counts.FP++
			}
			res.Matches = append(res.Matches, RealVulnMatch{
				Classification: class,
				GroundTruthID:  e.ID,
				Finding:        f,
				HasFinding:     true,
			})
			continue
		}

		if hit, ok := realVulnNonScoringHit(f, nonScoring); ok {
			res.Matches = append(res.Matches, RealVulnMatch{
				Classification: MatchNS,
				GroundTruthID:  hit,
				Finding:        f,
				HasFinding:     true,
			})
			continue
		}

		res.Counts.FP++
		res.Matches = append(res.Matches, RealVulnMatch{
			Classification: MatchFP,
			Finding:        f,
			HasFinding:     true,
		})
	}

	for i, e := range scored {
		if matched[i] {
			continue
		}
		if e.IsVulnerable {
			res.Counts.FN++
			res.Matches = append(res.Matches, RealVulnMatch{
				Classification: MatchFN,
				GroundTruthID:  e.ID,
			})
			continue
		}
		res.Counts.TN++
		res.Matches = append(res.Matches, RealVulnMatch{
			Classification: MatchTN,
			GroundTruthID:  e.ID,
		})
	}

	for _, e := range nonScoring {
		res.Matches = append(res.Matches, RealVulnMatch{
			Classification: MatchNS,
			GroundTruthID:  e.ID,
		})
	}

	for _, e := range scored {
		if e.IsVulnerable {
			continue
		}
		res.Traps.Traps++
	}
	for _, m := range res.Matches {
		if !m.HasFinding || m.GroundTruthID == "" {
			continue
		}
		for _, e := range scored {
			if e.ID != m.GroundTruthID || e.IsVulnerable {
				continue
			}
			res.Traps.TrapsHit++
			break
		}
	}
	res.Traps.TrapsSilent = res.Traps.Traps - res.Traps.TrapsHit

	return res
}

// realVulnNonScoringHit returns the identifier of the non-scoring entry
// whose region holds the finding.
//
// The check is deliberately location-only: the status of such a location is
// unsettled, so any report on it is equally unsettled and must not be charged
// as a false positive. A finding without a line is never withheld, because
// the file-level leniency of the line window would let any entry of the same
// file withhold it.
func realVulnNonScoringHit(f Finding, entries []RealVulnEntry) (string, bool) {
	if f.Line <= 0 {
		return "", false
	}
	hit := ""
	for _, e := range entries {
		if !realVulnLocationMatches(f, e.File, e.StartLine, e.EndLine) {
			continue
		}
		if hit == "" || e.ID < hit {
			hit = e.ID
		}
	}
	return hit, hit != ""
}

// RealVulnManifestRepo is the entry of one repository in the benchmark
// manifest.
type RealVulnManifestRepo struct {
	// RepoURL is the location of the repository.
	RepoURL string
	// CommitSHA is the commit the ground truth was written against.
	CommitSHA string
	// Language is the main language of the repository.
	Language string
	// VulnerableFindings is the number of real vulnerabilities.
	VulnerableFindings int
	// FalsePositiveTraps is the number of false-positive traps.
	FalsePositiveTraps int
}

// RealVulnManifest is the manifest of the benchmark archive.
type RealVulnManifest struct {
	// Version is the version of the benchmark.
	Version string
	// GroundTruthContentHash is the digest the benchmark computes over
	// its ground-truth files. It pins the content of the corpus
	// independently of the archive.
	GroundTruthContentHash string
	// Repos holds the repositories of the manifest, keyed by their
	// identifier.
	Repos map[string]RealVulnManifestRepo
}

// realVulnManifestFile is the JSON layout of the benchmark manifest.
type realVulnManifestFile struct {
	BenchmarkVersion       string `json:"benchmark_version"`
	GroundTruthContentHash string `json:"ground_truth_content_hash"`
	Repos                  map[string]struct {
		RepoURL            string `json:"repo_url"`
		CommitSHA          string `json:"commit_sha"`
		Language           string `json:"language"`
		VulnerableFindings int    `json:"vulnerable_findings"`
		FalsePositiveTraps int    `json:"false_positive_traps"`
	} `json:"repos"`
}

// ParseRealVulnManifest parses the benchmark manifest.
func ParseRealVulnManifest(r io.Reader) (*RealVulnManifest, error) {
	var doc realVulnManifestFile
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("external: parse the RealVuln manifest: %w", err)
	}
	m := &RealVulnManifest{
		Version:                doc.BenchmarkVersion,
		GroundTruthContentHash: doc.GroundTruthContentHash,
		Repos:                  make(map[string]RealVulnManifestRepo, len(doc.Repos)),
	}
	for id, r := range doc.Repos {
		m.Repos[id] = RealVulnManifestRepo{
			RepoURL:            r.RepoURL,
			CommitSHA:          r.CommitSHA,
			Language:           r.Language,
			VulnerableFindings: r.VulnerableFindings,
			FalsePositiveTraps: r.FalsePositiveTraps,
		}
	}
	return m, nil
}

// RealVulnRepo is one repository of the corpus: its ground truth and its
// sources.
type RealVulnRepo struct {
	// RepoID is the identifier of the repository.
	RepoID string
	// RepoURL is the location the repository was cloned from.
	RepoURL string
	// CommitSHA is the commit the ground truth was written against.
	CommitSHA string
	// Language is the main language of the repository.
	Language string
	// GroundTruth holds the entries of the repository.
	GroundTruth []RealVulnEntry
	// SourceDir is the directory the sources were extracted into.
	SourceDir string
}

// RealVulnCorpus is a fetched RealVuln corpus.
type RealVulnCorpus struct {
	// Digest is the SHA-256 of the benchmark archive the ground truth was
	// read from.
	Digest string
	// Version is the version of the benchmark.
	Version string
	// GroundTruthContentHash is the digest the benchmark computes over
	// its ground-truth files.
	GroundTruthContentHash string
	// Repos holds the repositories of the corpus.
	Repos []RealVulnRepo
}

// FetchRealVuln downloads the RealVuln corpus into the cache directory.
//
// The corpus is two things: the benchmark archive, which carries the ground
// truth and the manifest, and the source repositories the ground truth
// refers to. Both are fetched at their pinned revision, and the digest of the
// archive is recorded.
//
// Only the repositories of the requested language are fetched; "python"
// selects the ones the AISAI engines can read. In offline mode nothing is
// downloaded and only what the cache already holds is used.
func FetchRealVuln(ctx context.Context, cacheDir string, language string, offline bool) (*RealVulnCorpus, error) {
	if cacheDir == "" {
		cacheDir = DefaultCacheDir
	}
	rvCache := filepath.Join(cacheDir, RealVulnBenchmarkName)

	archive, err := Fetch(ctx, rvCache, Artefact{
		Name: RealVulnBenchmarkArchive,
		URL:  RealVulnBenchmarkURL,
	}, offline)
	if err != nil {
		return nil, err
	}

	extractDir := filepath.Join(rvCache, "benchmark")
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

	manifest, err := readManifest(filepath.Join(root, "benchmark-manifest.json"))
	if err != nil {
		return nil, err
	}

	corpus := &RealVulnCorpus{
		Digest:                 archive.SHA256,
		Version:                manifest.Version,
		GroundTruthContentHash: manifest.GroundTruthContentHash,
	}

	ids := make([]string, 0, len(manifest.Repos))
	for id, r := range manifest.Repos {
		if language != "" && !strings.EqualFold(r.Language, language) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		repo := manifest.Repos[id]
		gt, err := readGroundTruth(filepath.Join(root, "ground-truth", id, "ground-truth.json"))
		if err != nil {
			return nil, err
		}
		sourceDir, err := fetchRealVulnSource(ctx, rvCache, id, repo, offline)
		if err != nil {
			return nil, err
		}
		corpus.Repos = append(corpus.Repos, RealVulnRepo{
			RepoID:      id,
			RepoURL:     repo.RepoURL,
			CommitSHA:   repo.CommitSHA,
			Language:    repo.Language,
			GroundTruth: gt.Entries,
			SourceDir:   sourceDir,
		})
	}
	return corpus, nil
}

// fetchRealVulnSource downloads and extracts one target repository.
func fetchRealVulnSource(ctx context.Context, cacheDir, id string, repo RealVulnManifestRepo, offline bool) (string, error) {
	url, err := RealVulnSourceURL(repo.RepoURL, repo.CommitSHA)
	if err != nil {
		return "", err
	}
	name := id + ".tar.gz"
	res, err := Fetch(ctx, filepath.Join(cacheDir, "sources"), Artefact{Name: name, URL: url}, offline)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "sources", id)
	if _, err := os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("external: stat %s: %w", dir, err)
		}
		if err := ExtractTarGz(res.Path, dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// RealVulnSourceURL returns the pinned archive URL of a target repository.
//
// The URL addresses the commit the ground truth was written against, so the
// sources always match the ground truth.
func RealVulnSourceURL(repoURL, commitSHA string) (string, error) {
	if repoURL == "" || commitSHA == "" {
		return "", fmt.Errorf("external: the repository has no URL or no commit")
	}
	trimmed := strings.TrimSuffix(repoURL, ".git")
	parts := strings.Split(strings.TrimPrefix(trimmed, "https://github.com/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("external: %q is not a GitHub repository URL", repoURL)
	}
	return fmt.Sprintf("https://codeload.github.com/%s/%s/tar.gz/%s", parts[0], parts[1], commitSHA), nil
}

// readManifest reads the benchmark manifest of an extracted archive.
func readManifest(path string) (*RealVulnManifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("external: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseRealVulnManifest(f)
}

// readGroundTruth reads one ground-truth document.
func readGroundTruth(path string) (*RealVulnGroundTruth, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("external: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseRealVulnGroundTruth(f)
}

// findArchiveRoot returns the single directory a GitHub archive was
// extracted into.
func findArchiveRoot(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("external: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("external: %s holds no extracted archive", dir)
}

// ExtractTarGz extracts a gzipped tar archive into a directory.
//
// The paths of the archive are sanitised: an entry that would escape the
// destination directory is refused rather than written.
func ExtractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("external: open %s: %w", archivePath, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("external: read %s as gzip: %w", archivePath, err)
	}
	defer gz.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("external: create %s: %w", destDir, err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("external: read %s: %w", archivePath, err)
		}
		name := path.Clean("/" + filepath.ToSlash(hdr.Name))
		target := filepath.Join(destDir, filepath.FromSlash(name))
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("external: the archive entry %q escapes the destination", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("external: create %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("external: create %s: %w", filepath.Dir(target), err)
			}
			out, err := os.Create(target)
			if err != nil {
				return fmt.Errorf("external: create %s: %w", target, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("external: write %s: %w", target, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("external: close %s: %w", target, err)
			}
		default:
			// Symlinks and the other exotic entries are skipped: the
			// corpora hold regular files.
		}
	}
}

// PythonFiles returns the Python sources of a repository, keyed by their
// repository-relative path.
//
// The paths are the ones the ground truth uses, so that a finding and an
// entry can be compared without further normalisation.
func (r RealVulnRepo) PythonFiles() (map[string]string, error) {
	out := map[string]string{}
	root, err := findArchiveRoot(r.SourceDir)
	if err != nil {
		// The sources may already be rooted at the repository itself.
		root = r.SourceDir
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
		if !strings.HasSuffix(info.Name(), ".py") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[NormalizePath(rel)] = string(data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("external: read the sources of %s: %w", r.RepoID, err)
	}
	return out, nil
}

// RealVulnRun is the result of running one detector over the RealVuln
// corpus.
type RealVulnRun struct {
	// Detector is the name of the engine.
	Detector string
	// Files is the number of source files that were scanned.
	Files int
	// Findings is the number of findings the engine reported.
	Findings int
	// Counts is the confusion matrix of the run.
	Counts Counts
	// Traps reports the outcome on the false-positive traps.
	Traps RealVulnTrapCounts
	// Matches holds one entry per finding and per unmatched ground-truth
	// entry.
	Matches []RealVulnMatch
	// Errors holds the files that could not be scanned, with the reason.
	Errors []string
}

// RunRealVuln scans the corpus with a detector and matches the findings.
//
// The limit caps the number of source files that are scanned; zero scans
// every file. The ground truth is always complete: an entry whose file was
// not scanned is a false negative, which is the honest accounting for a file
// the engine could not read.
func RunRealVuln(ctx context.Context, corpus *RealVulnCorpus, det Detector, limit int) (*RealVulnRun, error) {
	run := &RealVulnRun{Detector: det.Name()}

	var findings []Finding
	var entries []RealVulnEntry
	files := 0

	for _, repo := range corpus.Repos {
		sources, err := repo.PythonFiles()
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(sources))
		for p := range sources {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if limit > 0 && files >= limit {
				break
			}
			files++
			found, err := det.Detect(ctx, p, sources[p])
			if err != nil {
				run.Errors = append(run.Errors, fmt.Sprintf("%s: %v", p, err))
				continue
			}
			findings = append(findings, found...)
		}
		entries = append(entries, repo.GroundTruth...)
	}

	run.Files = files
	run.Findings = len(findings)
	res := MatchRealVuln(findings, entries)
	run.Counts = res.Counts
	run.Traps = res.Traps
	run.Matches = res.Matches
	return run, nil
}

// SeverityPolicy is the severity threshold the AISAI rule engine applies in
// the application: every finding is counted.
//
// It mirrors bench.SeverityPolicy. The value is duplicated rather than
// imported so that this package stays independent of the internal benchmark
// harness.
const SeverityPolicy = model.SeverityLow

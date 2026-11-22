// Package advisory fetches known vulnerabilities for package versions.
//
// This is the layer the legacy AISAI never had: the tool was named for
// software supply chain vulnerability identification but contained no
// advisory data at all — it analysed code, never looked up whether a
// dependency was known-vulnerable.
//
// The OSV database is used in preference to others because it has a genuine
// offline mode: the PyPI slice of the database can be downloaded once and
// queried locally, so the air-gap property of the original project is
// preserved.
package advisory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

// Options configures an advisory client.
type Options struct {
	// BaseURL is the OSV API root. Defaults to https://api.osv.dev.
	BaseURL string
	// OfflineDBPath is the path to a downloaded OSV database directory.
	// When set and present, the client queries it instead of the network.
	OfflineDBPath string
	// Timeout bounds a single request.
	Timeout time.Duration
	// UserAgent identifies the client to OSV.
	UserAgent string
}

// Client queries for advisories.
type Client struct {
	opts    Options
	http    *http.Client
	offline bool
}

// New creates an advisory client.
//
// Setting OfflineDBPath puts the client in offline mode. The mode is a
// property of the configuration, not of the filesystem: a database that is
// configured but missing leaves the client offline and unavailable, because
// a client that was pointed at a local snapshot must not quietly fall back
// to the network.
func New(opts Options) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://api.osv.dev"
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "AISAI/0.1"
	}
	c := &Client{
		opts: opts,
		http: &http.Client{Timeout: opts.Timeout},
	}
	c.offline = opts.OfflineDBPath != ""
	return c
}

// Offline reports whether the client is running against a local database.
func (c *Client) Offline() bool { return c.offline }

// OSV query and response shapes.
type osvQuery struct {
	Package osvPackage `json:"package"`
	Version string     `json:"version"`
}

type osvPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

type osvResponse struct {
	Vulns []osvVuln `json:"vulns"`
}

type osvVuln struct {
	ID               string         `json:"id"`
	Summary          string         `json:"summary"`
	Details          string         `json:"details"`
	Aliases          []string       `json:"aliases"`
	Severity         []osvSeverity  `json:"severity"`
	DatabaseSpecific map[string]any `json:"database_specific"`
	Affected         []osvAffected  `json:"affected"`
	References       []osvReference `json:"references"`
}

type osvSeverity struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}

type osvAffected struct {
	Package  osvPackage `json:"package"`
	Ranges   []osvRange `json:"ranges"`
	Versions []string   `json:"versions"`
}

type osvRange struct {
	Type   string          `json:"type"`
	Events []osvRangeEvent `json:"events"`
}

type osvRangeEvent struct {
	Introduced   string `json:"introduced"`
	Fixed        string `json:"fixed"`
	LastAffected string `json:"last_affected"`
}

type osvReference struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Query returns the advisories affecting one package version.
func (c *Client) Query(ctx context.Context, name, version string) ([]model.Advisory, error) {
	return c.QueryFor(ctx, "PyPI", name, version)
}

// QueryFor returns the advisories affecting one package version of an OSV
// ecosystem ("PyPI", "npm", "crates.io").
func (c *Client) QueryFor(ctx context.Context, ecosystem, name, version string) ([]model.Advisory, error) {
	if ecosystem == "" {
		ecosystem = "PyPI"
	}
	if version == "" {
		return nil, nil
	}
	if c.offline {
		if !dirExists(c.opts.OfflineDBPath) {
			// The database was configured but is not there. Answering with an
			// empty list would say "this package has no known vulnerabilities",
			// which is a different claim, and querying the network would break
			// the promise the configuration made.
			return nil, fmt.Errorf(
				"the offline advisory database %s does not exist: the client stays offline rather than querying the network",
				c.opts.OfflineDBPath)
		}
		return c.queryOffline(ctx, ecosystem, name, version)
	}
	return c.queryOnline(ctx, ecosystem, name, version)
}

// queryOnline queries the OSV API.
func (c *Client) queryOnline(ctx context.Context, ecosystem, name, version string) ([]model.Advisory, error) {
	body, err := json.Marshal(osvQuery{
		Package: osvPackage{Name: name, Ecosystem: ecosystem},
		Version: version,
	})
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(c.opts.BaseURL, "/") + "/v1/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.opts.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query osv: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("query osv: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var out osvResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode osv response: %w", err)
	}
	return convert(out.Vulns), nil
}

// queryOffline queries a downloaded OSV database.
//
// The official offline database is a zip of per-package JSON files. This
// implementation reads the directory form of that database: one JSON file
// per package under <db>/PyPI/<package>.json.
func (c *Client) queryOffline(ctx context.Context, ecosystem, name, version string) ([]model.Advisory, error) {
	path := filepath.Join(c.opts.OfflineDBPath, ecosystem, strings.ReplaceAll(name, "/", "%2f")+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out osvResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode offline osv record: %w", err)
	}
	// The offline record lists every affected version; keep only the
	// advisories whose ranges actually cover the requested version.
	return filterByVersion(out.Vulns, version), nil
}

// filterByVersion keeps only the vulnerabilities whose affected ranges
// include the given version.
func filterByVersion(vulns []osvVuln, version string) []model.Advisory {
	var out []model.Advisory
	for _, v := range vulns {
		if !affectsVersion(v, version) {
			continue
		}
		out = append(out, convert([]osvVuln{v})[0])
	}
	return out
}

// affectsVersion reports whether a vulnerability's affected ranges include
// the version.
func affectsVersion(v osvVuln, version string) bool {
	if len(v.Affected) == 0 {
		return true
	}
	for _, aff := range v.Affected {
		for _, ver := range aff.Versions {
			if ver == version {
				return true
			}
		}
		for _, r := range aff.Ranges {
			if r.Type != "ECOSYSTEM" && r.Type != "SEMVER" {
				continue
			}
			introduced := ""
			fixed := ""
			for _, e := range r.Events {
				if e.Introduced != "" {
					introduced = e.Introduced
				}
				if e.Fixed != "" {
					fixed = e.Fixed
				}
			}
			if introduced == "" {
				continue
			}
			if compareVersions(version, introduced) >= 0 {
				if fixed == "" || compareVersions(version, fixed) < 0 {
					return true
				}
			}
		}
	}
	return false
}

// compareVersions compares two PEP 440-ish version strings.
//
// The ordering is approximate — it handles the common numeric and
// pre-release forms — but it is only used to decide whether an advisory
// range covers a version, and a false negative here is safer than a false
// positive.
func compareVersions(a, b string) int {
	an := versionParts(a)
	bn := versionParts(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		var av, bv int
		if i < len(an) {
			av = an[i]
		}
		if i < len(bn) {
			bv = bn[i]
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimSpace(v)
	// Drop any epoch and local version segment.
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, "-"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, "!"); i >= 0 {
		v = v[i+1:]
	}
	var out []int
	for _, part := range strings.Split(v, ".") {
		n := 0
		ok := true
		for _, r := range part {
			if r < '0' || r > '9' {
				ok = false
				break
			}
			n = n*10 + int(r-'0')
		}
		if !ok {
			break
		}
		out = append(out, n)
	}
	return out
}

// convert maps OSV records onto the canonical advisory model.
func convert(vulns []osvVuln) []model.Advisory {
	out := make([]model.Advisory, 0, len(vulns))
	for _, v := range vulns {
		a := model.Advisory{
			ID:       v.ID,
			Source:   "osv",
			Summary:  firstNonEmpty(v.Summary, v.Details, v.ID),
			Severity: severityOf(v),
			URL:      referenceURL(v),
		}
		if len(v.Aliases) > 0 {
			// Prefer a CVE alias as the identifier when there is one, since
			// CVEs are what most other tools key on.
			for _, al := range v.Aliases {
				if strings.HasPrefix(al, "CVE-") {
					a.ID = al
					break
				}
			}
		}
		if ids, ok := v.DatabaseSpecific["cwe_ids"]; ok {
			a.CWE = toStringSlice(ids)
		}
		a.FixedIn = fixedVersions(v)
		out = append(out, a)
	}
	return out
}

// severityOf derives a normalized severity from an OSV record.
func severityOf(v osvVuln) model.Severity {
	// GHSA records carry a plain label in database_specific.severity.
	if s, ok := v.DatabaseSpecific["severity"].(string); ok {
		switch strings.ToUpper(s) {
		case "CRITICAL":
			return model.SeverityCritical
		case "HIGH":
			return model.SeverityHigh
		case "MODERATE", "MEDIUM":
			return model.SeverityMedium
		case "LOW":
			return model.SeverityLow
		}
	}
	// Otherwise look for a numeric CVSS base score.
	for _, s := range v.Severity {
		if n, ok := parseCVSSScore(s.Score); ok {
			return severityFromScore(n)
		}
	}
	return model.SeverityMedium
}

// parseCVSSScore extracts a base score from a CVSS string.
//
// OSV stores CVSS as a vector string ("CVSS:3.1/AV:N/AC:L/...") in some
// records and as a bare number in others.
func parseCVSSScore(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if !strings.Contains(s, "/") {
		var f float64
		if _, err := fmt.Sscanf(s, "%g", &f); err == nil {
			return f, true
		}
		return 0, false
	}
	// Compute the base score from a CVSS v3 vector.
	// Parse the raw metric values first: the privilege-required weight
	// depends on the scope, which may appear after it.
	var avRaw, acRaw, prRaw, uiRaw, sRaw, cRaw, iRaw, aRaw string
	for _, part := range strings.Split(s, "/") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "AV":
			avRaw = kv[1]
		case "AC":
			acRaw = kv[1]
		case "PR":
			prRaw = kv[1]
		case "UI":
			uiRaw = kv[1]
		case "S":
			sRaw = kv[1]
		case "C":
			cRaw = kv[1]
		case "I":
			iRaw = kv[1]
		case "A":
			aRaw = kv[1]
		}
	}
	if avRaw == "" && acRaw == "" && prRaw == "" {
		return 0, false
	}

	scopeUnchanged := sRaw != "C"
	av := cvssAV[avRaw]
	ac := cvssAC[acRaw]
	pr := cvssPR(!scopeUnchanged, prRaw)
	ui := cvssUI[uiRaw]
	c := cvssCIA[cRaw]
	i := cvssCIA[iRaw]
	a := cvssCIA[aRaw]

	iss := 1 - (1-c)*(1-i)*(1-a)
	var impact float64
	if scopeUnchanged {
		impact = 6.42 * iss
	} else {
		impact = 7.52*(iss-0.029) - 3.25*pow(iss-0.02, 15)
	}
	exploitability := 8.22 * av * ac * pr * ui
	var base float64
	if impact <= 0 {
		base = 0
	} else if scopeUnchanged {
		base = roundUp(min(impact+exploitability, 10))
	} else {
		base = roundUp(min(1.08*(impact+exploitability), 10))
	}
	return base, true
}

// CVSS v3.1 metric weights. Each metric has its own scale, so they cannot
// share a single lookup table.
var (
	cvssAV  = map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.20}
	cvssAC  = map[string]float64{"L": 0.77, "H": 0.44}
	cvssUI  = map[string]float64{"N": 0.85, "R": 0.62}
	cvssCIA = map[string]float64{"H": 0.56, "L": 0.22, "N": 0.0}
)

// cvssPR returns the privilege-required weight. It differs between
// Scope:Unchanged and Scope:Changed.
func cvssPR(scopeChanged bool, v string) float64 {
	switch v {
	case "N":
		return 0.85
	case "L":
		if scopeChanged {
			return 0.68
		}
		return 0.62
	case "H":
		if scopeChanged {
			return 0.50
		}
		return 0.27
	default:
		return 0
	}
}

func severityFromScore(score float64) model.Severity {
	switch {
	case score >= 9.0:
		return model.SeverityCritical
	case score >= 7.0:
		return model.SeverityHigh
	case score >= 4.0:
		return model.SeverityMedium
	case score > 0:
		return model.SeverityLow
	default:
		return model.SeverityInfo
	}
}

// fixedVersions lists the versions in which the vulnerability was fixed.
func fixedVersions(v osvVuln) []string {
	var out []string
	for _, aff := range v.Affected {
		for _, r := range aff.Ranges {
			for _, e := range r.Events {
				if e.Fixed != "" {
					out = append(out, e.Fixed)
				}
			}
		}
	}
	return out
}

func referenceURL(v osvVuln) string {
	for _, r := range v.References {
		if r.Type == "ADVISORY" || r.Type == "FIX" || r.Type == "WEB" {
			return r.URL
		}
	}
	return ""
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{t}
	default:
		return nil
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func pow(a, b float64) float64 {
	result := 1.0
	for i := 0; i < int(b); i++ {
		result *= a
	}
	return result
}

// roundUp rounds to one decimal place, as CVSS requires.
//
// This is the Roundup function of the CVSS v3.1 specification:
//
//	int_input = round(input * 100000)
//	if int_input % 10000 == 0:
//	    return int_input / 100000
//	else:
//	    return (floor(int_input / 10000) + 1) / 10
//
// The second branch is what makes the result strictly greater than the
// input. A simpler rounding would turn a vector that scores 6.96 into 7.0
// or 6.9 depending on the tie-break, and 6.96 is a **high** severity
// vulnerability while 6.9 is a medium one, so the difference decides how a
// finding is presented to the user.
func roundUp(v float64) float64 {
	scaled := math.Round(v * 100000)
	if scaled == 0 {
		return 0
	}
	if math.Mod(scaled, 10000) == 0 {
		return scaled / 100000
	}
	return (math.Floor(scaled/10000) + 1) / 10
}

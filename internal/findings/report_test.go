package findings

import (
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/model"
)

func TestWriteHTMLReportEscapesAndOrders(t *testing.T) {
	list := []model.Finding{
		{Rule: "low-one", Severity: "low", File: "a.py", StartLine: 1, EndLine: 1, Message: "m"},
		{Rule: "crit<script>", Severity: "critical", File: "b.py", StartLine: 2, EndLine: 4, Message: "x", Code: "<b>boom</b>", Confidence: 0.9},
	}
	data, err := WriteHTMLReport(list, ReportOptions{Package: "demo", Version: "1.0", AuditID: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if strings.Contains(out, "<script>") || strings.Contains(out, "<b>boom") {
		t.Fatal("report must escape finding text")
	}
	if strings.Index(out, "crit&lt;script&gt;") > strings.Index(out, "low-one") {
		t.Fatal("critical findings must come first")
	}
	if !strings.Contains(out, "demo") || !strings.Contains(out, "90%") {
		t.Fatalf("missing title or confidence: %s", out[:200])
	}
}

// The PDF export prints this page, so its print rules must keep findings
// whole, wrap code and use a light page.
func TestWriteHTMLReportIsLaidOutForPaper(t *testing.T) {
	out, err := WriteHTMLReport(nil, ReportOptions{Package: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	for _, want := range []string{"@media print", "break-inside:avoid", "white-space:pre-wrap", "print-color-adjust:exact", "@page"} {
		if !strings.Contains(html, want) {
			t.Errorf("the report has no %q rule", want)
		}
	}
}

// The report lists the dependencies' vulnerabilities with the chain that
// brings each one in, and says nothing about them when they were not checked.
func TestReportShowsVulnerableDependencies(t *testing.T) {
	adv := []model.Advisory{
		{ID: "RUSTSEC-2020-0071", Severity: "medium", Summary: "Segfault in time", Package: "pkg:cargo/time@0.1.43", Via: []string{"chrono", "time"}, FixedIn: []string{"0.2.23"}, URL: "https://osv.dev/RUSTSEC-2020-0071"},
		{ID: "CVE-1", Severity: "high", Summary: "Direct one", Package: "pkg:cargo/direct@1.0.0", Via: []string{"direct"}},
	}
	out, err := WriteHTMLReport(nil, ReportOptions{Package: "demo", Advisories: adv, AdvisoriesChecked: 12})
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	for _, want := range []string{"Vulnerable dependencies", "chrono → time", "Pulled in by", "time 0.1.43", "Fixed in", "0.2.23", "12 checked packages", "Direct dependency"} {
		if !strings.Contains(html, want) {
			t.Errorf("the report is missing %q", want)
		}
	}
	// The worse one comes first.
	if strings.Index(html, "CVE-1") > strings.Index(html, "RUSTSEC-2020-0071") {
		t.Error("the high severity advisory should be listed first")
	}

	none, err := WriteHTMLReport(nil, ReportOptions{Package: "demo", AdvisoriesChecked: 40})
	if err != nil || !strings.Contains(string(none), "No known vulnerabilities in 40 checked packages") {
		t.Errorf("a checked project with no advisories should say so: %v", err)
	}
	unchecked, _ := WriteHTMLReport(nil, ReportOptions{Package: "demo"})
	if strings.Contains(string(unchecked), "Vulnerable dependencies") {
		t.Error("a project that was not checked must not claim anything about its dependencies")
	}
}

package findings

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

// ReportOptions describe the audit a report is written for.
type ReportOptions struct {
	Package     string
	Version     string
	AuditID     string
	ToolVersion string
	Generated   time.Time
	// Advisories are the known vulnerabilities of the dependencies, with the
	// chain of packages that pulls each one in. AdvisoriesChecked is how many
	// packages were looked up; with Fetched false-looking zero values the
	// section is left out, because "not checked" is not "none found".
	Advisories        []model.Advisory
	AdvisoriesChecked int
}

type reportSeverity struct {
	Name  string
	Count int
	Pct   int
}

// reportAdvisory is one vulnerable dependency, ready to print.
type reportAdvisory struct {
	Severity   string
	ID         string
	URL        string
	Package    string
	Summary    string
	Via        string
	Transitive bool
	Fixed      string
}

type reportView struct {
	ReportOptions
	Title      string
	Date       string
	Total      int
	Open       int
	Severities []reportSeverity
	Findings   []model.Finding
	// Vulns is the dependencies' known vulnerabilities, worst first.
	Vulns         []reportAdvisory
	ShowVulns     bool
	TransitiveCnt int
}

var severityOrder = []model.Severity{"critical", "high", "medium", "low", "info"}

func severityRank(s model.Severity) int {
	for i, v := range severityOrder {
		if v == s {
			return i
		}
	}
	return len(severityOrder)
}

// WriteHTMLReport renders the findings as one self-contained HTML page in the
// look of the app (the Verdigris theme): a summary by severity, then every
// finding with its code, evidence and remediation. It uses no script and no
// external resource, so it can be mailed or archived as it is.
func WriteHTMLReport(list []model.Finding, opts ReportOptions) ([]byte, error) {
	if opts.Generated.IsZero() {
		opts.Generated = time.Now()
	}
	sorted := append([]model.Finding(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if a, b := severityRank(sorted[i].Severity), severityRank(sorted[j].Severity); a != b {
			return a < b
		}
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		return sorted[i].StartLine < sorted[j].StartLine
	})

	counts := map[model.Severity]int{}
	open := 0
	for _, f := range sorted {
		counts[f.Severity]++
		if f.State != "fixed" && f.State != "suppressed" {
			open++
		}
	}
	view := reportView{
		ReportOptions: opts,
		Title:         opts.Package,
		Date:          opts.Generated.UTC().Format("2 January 2006, 15:04 UTC"),
		Total:         len(sorted),
		Open:          open,
		Findings:      sorted,
	}
	if view.Title == "" {
		view.Title = "Audit " + opts.AuditID
	}
	if opts.AdvisoriesChecked > 0 || len(opts.Advisories) > 0 {
		view.ShowVulns = true
		advs := append([]model.Advisory(nil), opts.Advisories...)
		sort.SliceStable(advs, func(i, j int) bool {
			if a, b := severityRank(advs[i].Severity), severityRank(advs[j].Severity); a != b {
				return a < b
			}
			return advs[i].ID < advs[j].ID
		})
		for _, a := range advs {
			ra := reportAdvisory{
				Severity: string(a.Severity), ID: a.ID, Package: packageText(string(a.Package)),
				Summary: a.Summary, Transitive: len(a.Via) > 1,
			}
			if strings.HasPrefix(a.URL, "https://") || strings.HasPrefix(a.URL, "http://") {
				ra.URL = a.URL
			}
			if len(a.Via) > 0 {
				ra.Via = strings.Join(a.Via, " → ")
			}
			if len(a.FixedIn) > 0 {
				ra.Fixed = strings.Join(a.FixedIn, ", ")
			}
			if ra.Transitive {
				view.TransitiveCnt++
			}
			view.Vulns = append(view.Vulns, ra)
		}
	}
	for _, s := range severityOrder {
		pct := 0
		if len(sorted) > 0 {
			pct = counts[s] * 100 / len(sorted)
		}
		view.Severities = append(view.Severities, reportSeverity{Name: string(s), Count: counts[s], Pct: pct})
	}

	var buf bytes.Buffer
	if err := reportTemplate.Execute(&buf, view); err != nil {
		return nil, fmt.Errorf("render the report: %w", err)
	}
	return buf.Bytes(), nil
}

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"pct": func(f float64) int { return int(f*100 + 0.5) },
}).Parse(reportHTML))

const reportHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · AISAI report</title>
<style>
:root{--bg:#0a1112;--surface:#0f1819;--raised:#152123;--inset:#070c0d;--border:#223336;--border2:#35494c;
--fg:#dde8e3;--muted:#8ea5a0;--accent:#b4f23c;--critical:#ff7a6b;--high:#ff9f4a;--medium:#f0c93a;--low:#5fd3c4;--info:#8ea5a0;
--mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;--sans:system-ui,-apple-system,"Segoe UI",sans-serif}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.55 var(--sans)}
main{max-width:56rem;margin:0 auto;padding:3rem 1.5rem 4rem}
.brand{display:flex;align-items:center;gap:.6rem;color:var(--muted);font-size:.8rem;letter-spacing:.08em;text-transform:uppercase}
.brand i{width:.9rem;height:.9rem;border-radius:50%;background:var(--accent);display:inline-block}
h1{font-size:2rem;margin:.6rem 0 .2rem;letter-spacing:-.01em}
.meta{color:var(--muted);font-size:.85rem;font-family:var(--mono)}
.summary{display:grid;grid-template-columns:repeat(auto-fit,minmax(8rem,1fr));gap:.75rem;margin:2rem 0 1rem}
.card{background:var(--raised);border:1px solid var(--border);border-radius:.5rem;padding:.8rem 1rem}
.card b{display:block;font-size:1.7rem;line-height:1.1}
.card span{color:var(--muted);font-size:.75rem;text-transform:uppercase;letter-spacing:.06em}
.bar{display:flex;height:.6rem;border-radius:.3rem;overflow:hidden;background:var(--inset);margin-bottom:2.5rem}
.bar div{min-width:0}
.s-critical{--c:var(--critical)}.s-high{--c:var(--high)}.s-medium{--c:var(--medium)}.s-low{--c:var(--low)}.s-info{--c:var(--info)}
.bar div{background:var(--c)}
.card.sev{border-top:3px solid var(--c)}
.finding{background:var(--surface);border:1px solid var(--border);border-left:4px solid var(--c,var(--info));border-radius:.5rem;padding:1rem 1.2rem;margin:0 0 1rem;break-inside:avoid}
.finding h3{margin:0 0 .3rem;font-size:1rem}
.badge{display:inline-block;font:600 .7rem var(--mono);text-transform:uppercase;letter-spacing:.05em;padding:.1rem .45rem;border-radius:.25rem;color:var(--c);border:1px solid var(--c);margin-right:.4rem}
.badge.plain{color:var(--muted);border-color:var(--border2)}
.where{font:.8rem var(--mono);color:var(--muted);margin:.2rem 0 .6rem}
pre{background:var(--inset);border:1px solid var(--border);border-radius:.4rem;padding:.7rem .9rem;overflow:auto;font:.8rem/1.5 var(--mono);margin:.6rem 0}
.note{margin:.5rem 0 0;color:var(--muted)}.note strong{color:var(--fg)}
h2{font-size:.8rem;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin:2.5rem 0 1rem}
footer{margin-top:3rem;color:var(--muted);font-size:.75rem;border-top:1px solid var(--border);padding-top:1rem}
/* On paper (and in the PDF export): a light page, severity colours dark enough
   to read in print, code that wraps instead of scrolling, and no finding cut
   in two by a page break. */
@page{margin:16mm 14mm 18mm}
@media print{
:root{--bg:#fff;--surface:#fff;--raised:#f5f8f7;--inset:#f3f6f5;--border:#d0dbd8;--border2:#b7c6c2;
--fg:#121a1b;--muted:#566663;--accent:#4d7c0f;--critical:#b42318;--high:#b54708;--medium:#8a6a00;--low:#0e7466;--info:#5b6b68}
html,body{background:#fff;color:var(--fg)}
*{-webkit-print-color-adjust:exact;print-color-adjust:exact}
body{font-size:10.5pt;line-height:1.5}
main{max-width:none;padding:0}
h1{font-size:20pt;margin-top:.3rem}
.summary{grid-template-columns:repeat(7,1fr);gap:.4rem;margin:1.2rem 0 .8rem}
.card{padding:.5rem .6rem}
.card b{font-size:14pt}
.card span{font-size:6.5pt;letter-spacing:.03em;white-space:nowrap}
.bar{margin-bottom:1.5rem}
h2{margin:1.5rem 0 .8rem;break-after:avoid}
.finding{break-inside:avoid;page-break-inside:avoid;padding:.7rem .9rem;margin-bottom:.7rem}
.finding h3{break-after:avoid}
pre{white-space:pre-wrap;word-break:break-word;overflow:visible;font-size:8.5pt;break-inside:avoid}
footer{margin-top:1.5rem}
}
</style>
</head>
<body>
<main>
<div class="brand"><i></i> aisai security report</div>
<h1>{{.Title}}{{if .Version}} <span style="color:var(--muted);font-weight:400">{{.Version}}</span>{{end}}</h1>
<div class="meta">{{.Date}}{{if .ToolVersion}} · aisai {{.ToolVersion}}{{end}}{{if .AuditID}} · audit {{.AuditID}}{{end}}</div>

<div class="summary">
<div class="card"><b>{{.Total}}</b><span>findings</span></div>
<div class="card"><b>{{.Open}}</b><span>still open</span></div>
{{range .Severities}}<div class="card sev s-{{.Name}}"><b>{{.Count}}</b><span>{{.Name}}</span></div>{{end}}
</div>
<div class="bar" role="img" aria-label="Findings by severity">{{range .Severities}}{{if .Count}}<div class="s-{{.Name}}" style="width:{{.Pct}}%"></div>{{end}}{{end}}</div>

{{if .ShowVulns}}
<h2>Vulnerable dependencies</h2>
{{if .Vulns}}
<p class="note">{{len .Vulns}} known {{if eq (len .Vulns) 1}}vulnerability{{else}}vulnerabilities{{end}} in {{.AdvisoriesChecked}} checked packages{{if .TransitiveCnt}}; {{.TransitiveCnt}} of them in packages pulled in indirectly, with the chain that brings each in{{end}}. Source: OSV (osv.dev).</p>
{{range .Vulns}}
<article class="finding s-{{.Severity}}">
<h3><span class="badge s-{{.Severity}}">{{.Severity}}</span>{{if .URL}}<a href="{{.URL}}" style="color:inherit">{{.ID}}</a>{{else}}{{.ID}}{{end}} <span style="color:var(--muted);font-weight:400">{{.Package}}</span></h3>
{{if .Summary}}<div>{{.Summary}}</div>{{end}}
{{if .Via}}<p class="note"><strong>{{if .Transitive}}Pulled in by.{{else}}Direct dependency.{{end}}</strong> {{.Via}}</p>{{end}}
{{if .Fixed}}<p class="note"><strong>Fixed in.</strong> {{.Fixed}}</p>{{end}}
</article>
{{end}}
{{else}}
<p class="note">No known vulnerabilities in {{.AdvisoriesChecked}} checked packages. This covers the advisories in OSV, not unknown flaws.</p>
{{end}}
{{end}}

<h2>Findings</h2>
{{range .Findings}}
<article class="finding s-{{.Severity}}">
<h3><span class="badge s-{{.Severity}}">{{.Severity}}</span>{{if .CWE}}<span class="badge plain">{{.CWE}}</span>{{end}}{{.Rule}}</h3>
<div class="where">{{.File}}:{{.StartLine}}{{if gt .EndLine .StartLine}}–{{.EndLine}}{{end}} · confidence {{pct .Confidence}}% · {{.State}}{{if .Reachability}} · {{.Reachability}}{{end}}</div>
<div>{{.Message}}</div>
{{if .Code}}<pre>{{.Code}}</pre>{{end}}
{{if .Evidence.Note}}<p class="note"><strong>Evidence.</strong> {{.Evidence.Note}}</p>{{end}}
{{if .Remediation}}<p class="note"><strong>Fix.</strong> {{.Remediation}}</p>{{end}}
</article>
{{else}}
<p class="note">No findings.</p>
{{end}}
<footer>Generated by aisai. Findings come from static rules and, where noted, a language model; review them before acting.</footer>
</main>
</body>
</html>
`

// packageText turns a package URL into "name version": "pkg:cargo/time@0.1.43"
// reads "time 0.1.43".
func packageText(purl string) string {
	body := strings.TrimPrefix(purl, "pkg:")
	if i := strings.Index(body, "/"); i >= 0 {
		body = body[i+1:]
	}
	if at := strings.LastIndex(body, "@"); at > 0 {
		return body[:at] + " " + body[at+1:]
	}
	return body
}

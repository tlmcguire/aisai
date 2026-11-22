package findings

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
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
}

type reportSeverity struct {
	Name  string
	Count int
	Pct   int
}

type reportView struct {
	ReportOptions
	Title      string
	Date       string
	Total      int
	Open       int
	Severities []reportSeverity
	Findings   []model.Finding
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
@media print{body{background:#fff;color:#111}:root{--surface:#fff;--raised:#f4f7f6;--inset:#f0f3f2;--border:#d3dedb;--fg:#111;--muted:#555}}
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

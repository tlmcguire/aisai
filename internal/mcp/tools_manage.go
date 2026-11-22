package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/sast"
)

// The tools in this file let an agent manage audits and triage findings, not
// only read them: list the audits on disk, switch between them, record a
// verdict on a finding, free the downloaded sources, and discover which
// languages and rules the scanner covers.

func registerManagementTools(s *Server, t *Toolset) {
	s.Register(Tool{
		Name: "list_audits",
		Description: "List every audit this AISAI workspace holds, newest first: id, project, version, where the code came from (PyPI or a local folder), status, finding count and the size of its downloaded sources. " +
			"Use this to resume earlier work; pass an id to select_audit to make it current.",
		InputSchema: ObjectSchema(map[string]any{}),
		Handler:     t.listAudits,
	})

	s.Register(Tool{
		Name:        "select_audit",
		Description: "Make an existing audit the current one, so later tools can be called without an audit_id.",
		InputSchema: ObjectSchema(map[string]any{
			"audit_id": StringProp("The audit to select, as printed by list_audits."),
		}, "audit_id"),
		Handler: t.selectAudit,
	})

	s.Register(Tool{
		Name: "set_finding_state",
		Description: "Record a triage verdict on a finding: confirmed (a real issue), suppressed (accepted risk or false positive, until a date), fixed, or open again. " +
			"The verdict is stored with the audit and shows in the desktop app and in exported reports.",
		InputSchema: ObjectSchema(map[string]any{
			"finding_id":       StringProp("The id of the finding, as printed by list_findings."),
			"state":            StringProp("The new state: open, confirmed, suppressed or fixed."),
			"suppressed_until": StringProp("For suppressed: the date the suppression lapses, YYYY-MM-DD. Defaults to 30 days from now."),
			"audit_id":         StringProp("The audit the finding belongs to. Defaults to the current audit."),
		}, "finding_id", "state"),
		Handler: t.setFindingState,
	})

	s.Register(Tool{
		Name: "delete_sources",
		Description: "Delete the downloaded source code of an audit to free disk space. The findings, the triage verdicts and the report are kept; only the sandbox of downloaded code is removed, so get_source and scans stop working for that audit. " +
			"This cannot be undone without re-running scan_package.",
		InputSchema: ObjectSchema(map[string]any{
			"audit_id": StringProp("The audit whose sources to delete. Defaults to the current audit."),
		}),
		Handler: t.deleteSources,
	})

	s.Register(Tool{
		Name:        "list_languages",
		Description: "List the source languages AISAI parses and scans, with the file extensions each one covers.",
		InputSchema: ObjectSchema(map[string]any{}),
		Handler:     t.listLanguages,
	})

	s.Register(Tool{
		Name: "list_rules",
		Description: "List the static analysis rules: id, name, CWE, severity and what each one detects. " +
			"Use this to explain a finding's rule or to decide whether a class of bug is covered.",
		InputSchema: ObjectSchema(map[string]any{}),
		Handler:     t.listRules,
	})
}

func (t *Toolset) listAudits(_ context.Context, _ map[string]any) (string, error) {
	if t.manager == nil {
		return "", errors.New("no audit manager configured")
	}
	audits := t.manager.List()
	if len(audits) == 0 {
		return "No audits yet. Call scan_package with a package name or a folder path.", nil
	}
	sort.Slice(audits, func(i, j int) bool { return audits[i].UpdatedAt.After(audits[j].UpdatedAt) })
	current := t.CurrentAudit()
	var b strings.Builder
	fmt.Fprintf(&b, "%d audit(s):\n", len(audits))
	for _, a := range audits {
		mark := " "
		if a.ID == current {
			mark = "*"
		}
		source := "pypi"
		if a.Source != "" {
			source = a.Source
		}
		sources := humanBytes(a.SandboxBytes)
		if a.SourcesDeleted {
			sources = "deleted"
		}
		name := a.Package
		if a.Version != "" {
			name += " " + a.Version
		}
		fmt.Fprintf(&b, "%s %s  %s  [%s] %s, %d finding(s), sources %s\n",
			mark, a.ID, name, source, a.Status, a.FindingCount, sources)
	}
	b.WriteString("(* = current audit)")
	return b.String(), nil
}

func (t *Toolset) selectAudit(_ context.Context, args map[string]any) (string, error) {
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	t.SetCurrentAudit(id)
	return "Current audit is now " + id + ".", nil
}

func (t *Toolset) setFindingState(ctx context.Context, args map[string]any) (string, error) {
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	store := t.findingsStore()
	if store == nil {
		return "", errors.New("no findings store configured")
	}
	fid := strings.TrimSpace(ArgString(args, "finding_id"))
	state := strings.ToLower(strings.TrimSpace(ArgString(args, "state")))
	switch state {
	case "open", "confirmed", "suppressed", "fixed":
	default:
		return "", fmt.Errorf("state must be open, confirmed, suppressed or fixed, not %q", state)
	}
	var until *time.Time
	if state == "suppressed" {
		day := time.Now().UTC().AddDate(0, 0, 30)
		if v := strings.TrimSpace(ArgString(args, "suppressed_until")); v != "" {
			parsed, perr := time.Parse("2006-01-02", v)
			if perr != nil {
				return "", fmt.Errorf("suppressed_until must be YYYY-MM-DD: %w", perr)
			}
			day = parsed
		}
		until = &day
	}
	if err := store.SetState(ctx, id, fid, state, until); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Finding %s is now %s.", fid, state)
	if until != nil {
		msg = fmt.Sprintf("Finding %s is suppressed until %s.", fid, until.Format("2006-01-02"))
	}
	return msg, nil
}

func (t *Toolset) deleteSources(ctx context.Context, args map[string]any) (string, error) {
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	freed, err := t.manager.DeleteSources(ctx, id)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted the sources of %s and freed %s. Findings and report are kept.", id, humanBytes(freed)), nil
}

func (t *Toolset) listLanguages(_ context.Context, _ map[string]any) (string, error) {
	var b strings.Builder
	langs := ast.Languages()
	fmt.Fprintf(&b, "%d languages:\n", len(langs))
	for _, l := range langs {
		fmt.Fprintf(&b, "- %s (%s): %s\n", l.DisplayName, l.ID, strings.Join(l.Extensions, " "))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (t *Toolset) listRules(_ context.Context, _ map[string]any) (string, error) {
	rules := append(sast.Rules(), sast.LanguageRules()...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	var b strings.Builder
	fmt.Fprintf(&b, "%d rules:\n", len(rules))
	for _, r := range rules {
		fmt.Fprintf(&b, "- %s  %s  %s  %s: %s\n", r.ID, r.CWE, r.Severity, r.Name, r.Description)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// humanBytes renders a byte count for a person.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

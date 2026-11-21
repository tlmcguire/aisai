// Package findings persists vulnerability findings and exports them in
// interchange formats.
//
// Storage is SQLite rather than loose JSON files. The legacy implementation
// wrote one JSON file per analysed file into a shared results directory,
// which meant (a) every scan wiped the previous results, and (b) files with
// the same basename in different packages overwrote each other. Both are
// impossible here: the primary key is scoped to the audit, and a scan only
// upserts.
package findings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tlmcguire/aisai/internal/model"
)

// Store is the findings database.
type Store struct {
	db *sql.DB
}

// Filter narrows a findings query.
type Filter struct {
	Severities []string
	CWEs       []string
	Tools      []string
	Files      []string
	States     []string
	Packages   []string
	Limit      int
	Offset     int
}

// Open opens (creating if needed) the findings database at path.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create findings dir: %w", err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open findings db: %w", err)
	}
	// SQLite tolerates concurrent readers poorly with many writers; a modest
	// pool keeps things predictable without starving reads.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)

	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS findings (
    id              TEXT    NOT NULL,
    audit_id        TEXT    NOT NULL,
    rule            TEXT    NOT NULL,
    tool            TEXT    NOT NULL,
    cwe             TEXT    NOT NULL DEFAULT '',
    severity        TEXT    NOT NULL,
    confidence      REAL    NOT NULL DEFAULT 0,
    file            TEXT    NOT NULL,
    start_line      INTEGER NOT NULL DEFAULT 0,
    end_line        INTEGER NOT NULL DEFAULT 0,
    code            TEXT    NOT NULL DEFAULT '',
    message         TEXT    NOT NULL DEFAULT '',
    evidence_source TEXT    NOT NULL DEFAULT '',
    evidence_sink   TEXT    NOT NULL DEFAULT '',
    evidence_flow   TEXT    NOT NULL DEFAULT '[]',
    evidence_note   TEXT    NOT NULL DEFAULT '',
    remediation     TEXT    NOT NULL DEFAULT '',
    patch           TEXT    NOT NULL DEFAULT '',
    reachability    TEXT    NOT NULL DEFAULT '',
    state           TEXT    NOT NULL DEFAULT 'open',
    suppressed_until TEXT,
    pkg_purl        TEXT    NOT NULL DEFAULT '',
    verified        INTEGER NOT NULL DEFAULT 0,
    verifier_note   TEXT    NOT NULL DEFAULT '',
    updated_at      TEXT    NOT NULL,
    PRIMARY KEY (audit_id, id)
);
CREATE INDEX IF NOT EXISTS idx_findings_audit  ON findings(audit_id);
CREATE INDEX IF NOT EXISTS idx_findings_file   ON findings(audit_id, file);
CREATE INDEX IF NOT EXISTS idx_findings_sev    ON findings(audit_id, severity);
CREATE INDEX IF NOT EXISTS idx_findings_cwe    ON findings(audit_id, cwe);

CREATE TABLE IF NOT EXISTS scans (
    audit_id   TEXT    NOT NULL,
    scan_id    TEXT    NOT NULL,
    engine     TEXT    NOT NULL,
    started_at TEXT    NOT NULL,
    ended_at   TEXT,
    finding_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (audit_id, scan_id)
);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate findings db: %w", err)
	}
	return nil
}

// Upsert inserts or updates findings for an audit.
//
// On conflict the technical fields are refreshed but the triage state is
// preserved: a user who suppressed or confirmed a finding should not have
// that decision undone by a re-scan.
func (s *Store) Upsert(ctx context.Context, auditID string, fs []model.Finding) (int, error) {
	if len(fs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO findings (
    id, audit_id, rule, tool, cwe, severity, confidence, file,
    start_line, end_line, code, message,
    evidence_source, evidence_sink, evidence_flow, evidence_note,
    remediation, patch, reachability, state, suppressed_until,
    pkg_purl, verified, verifier_note, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(audit_id, id) DO UPDATE SET
    rule=excluded.rule,
    tool=excluded.tool,
    cwe=excluded.cwe,
    severity=excluded.severity,
    confidence=excluded.confidence,
    file=excluded.file,
    start_line=excluded.start_line,
    end_line=excluded.end_line,
    code=excluded.code,
    message=excluded.message,
    evidence_source=excluded.evidence_source,
    evidence_sink=excluded.evidence_sink,
    evidence_flow=excluded.evidence_flow,
    evidence_note=excluded.evidence_note,
    remediation=excluded.remediation,
    patch=excluded.patch,
    reachability=excluded.reachability,
    pkg_purl=excluded.pkg_purl,
    verified=excluded.verified,
    verifier_note=excluded.verifier_note,
    updated_at=excluded.updated_at
`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, f := range fs {
		flow, _ := json.Marshal(f.Evidence.Flow)
		state := f.State
		if state == "" {
			state = model.StateOpen
		}
		var suppressed any
		if f.SuppressedUntil != nil {
			suppressed = f.SuppressedUntil.UTC().Format(time.RFC3339Nano)
		}
		_, err := stmt.ExecContext(ctx,
			f.ID, auditID, f.Rule, f.Tool, f.CWE, string(f.Severity), f.Confidence,
			model.RelPath(f.File), f.StartLine, f.EndLine, f.Code, f.Message,
			f.Evidence.Source, f.Evidence.Sink, string(flow), f.Evidence.Note,
			f.Remediation, f.Patch, f.Reachability, state, suppressed,
			string(f.Package), boolToInt(f.Verified), f.VerifierNote, now,
		)
		if err != nil {
			return 0, fmt.Errorf("upsert finding %s: %w", f.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(fs), nil
}

// List returns findings for an audit, most severe first.
func (s *Store) List(ctx context.Context, auditID string, f Filter) ([]model.Finding, error) {
	var (
		where = []string{"audit_id = ?"}
		args  = []any{auditID}
	)
	if len(f.Severities) > 0 {
		where = append(where, "severity IN ("+placeholders(len(f.Severities))+")")
		args = append(args, toAnySlice(f.Severities)...)
	}
	if len(f.CWEs) > 0 {
		where = append(where, "cwe IN ("+placeholders(len(f.CWEs))+")")
		args = append(args, toAnySlice(f.CWEs)...)
	}
	if len(f.Tools) > 0 {
		where = append(where, "tool IN ("+placeholders(len(f.Tools))+")")
		args = append(args, toAnySlice(f.Tools)...)
	}
	if len(f.Files) > 0 {
		where = append(where, "file IN ("+placeholders(len(f.Files))+")")
		args = append(args, toAnySlice(f.Files)...)
	}
	if len(f.States) > 0 {
		where = append(where, "state IN ("+placeholders(len(f.States))+")")
		args = append(args, toAnySlice(f.States)...)
	}
	if len(f.Packages) > 0 {
		where = append(where, "pkg_purl IN ("+placeholders(len(f.Packages))+")")
		args = append(args, toAnySlice(f.Packages)...)
	}

	// Severity ordering: critical, high, medium, low, info.
	order := `CASE severity
    WHEN 'critical' THEN 5 WHEN 'high' THEN 4 WHEN 'medium' THEN 3
    WHEN 'low' THEN 2 WHEN 'info' THEN 1 ELSE 0 END DESC,
    confidence DESC, file ASC, start_line ASC, rule ASC`

	q := "SELECT " + findingColumns + " FROM findings WHERE " +
		strings.Join(where, " AND ") + " ORDER BY " + order

	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
		if f.Offset > 0 {
			q += " OFFSET ?"
			args = append(args, f.Offset)
		}
	} else if f.Offset > 0 {
		q += " LIMIT -1 OFFSET ?"
		args = append(args, f.Offset)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Finding
	for rows.Next() {
		var (
			fd       model.Finding
			severity string
			flow     string
			pkg      string
			state    string
			supp     sql.NullString
			verified int
		)
		if err := rows.Scan(
			&fd.ID, &fd.Rule, &fd.Tool, &fd.CWE, &severity, &fd.Confidence, &fd.File,
			&fd.StartLine, &fd.EndLine, &fd.Code, &fd.Message,
			&fd.Evidence.Source, &fd.Evidence.Sink, &flow, &fd.Evidence.Note,
			&fd.Remediation, &fd.Patch, &fd.Reachability, &state, &supp,
			&pkg, &verified, &fd.VerifierNote,
		); err != nil {
			return nil, err
		}
		fd.Severity = model.Severity(severity)
		fd.State = state
		fd.Package = model.PURL(pkg)
		fd.Verified = verified != 0
		if supp.Valid && supp.String != "" {
			if t, err := time.Parse(time.RFC3339Nano, supp.String); err == nil {
				fd.SuppressedUntil = &t
			}
		}
		var fl []string
		if flow != "" {
			_ = json.Unmarshal([]byte(flow), &fl)
		}
		fd.Evidence.Flow = fl
		out = append(out, fd)
	}
	return out, rows.Err()
}

// Get returns a single finding.
func (s *Store) Get(ctx context.Context, auditID, id string) (*model.Finding, error) {
	list, err := s.List(ctx, auditID, Filter{})
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

// ErrNotFound is returned when a finding or audit does not exist.
var ErrNotFound = fmt.Errorf("not found")

// SetState updates the triage state of a finding.
func (s *Store) SetState(ctx context.Context, auditID, id, state string, until *time.Time) error {
	switch state {
	case model.StateOpen, model.StateConfirmed, model.StateSuppressed, model.StateFixed:
	default:
		return fmt.Errorf("invalid state %q", state)
	}
	if state == model.StateSuppressed && until == nil {
		return fmt.Errorf("suppressedUntil is required when state is suppressed")
	}
	var supp any
	if until != nil {
		supp = until.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE findings SET state = ?, suppressed_until = ?, updated_at = ? WHERE audit_id = ? AND id = ?`,
		state, supp, time.Now().UTC().Format(time.RFC3339Nano), auditID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountBySeverity returns the number of findings per severity.
func (s *Store) CountBySeverity(ctx context.Context, auditID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT severity, COUNT(*) FROM findings WHERE audit_id = ? GROUP BY severity`, auditID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var sev string
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			return nil, err
		}
		out[sev] = n
	}
	return out, rows.Err()
}

// Total returns the number of findings for an audit.
func (s *Store) Total(ctx context.Context, auditID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM findings WHERE audit_id = ?`, auditID).Scan(&n)
	return n, err
}

// FilesWithFindings returns the distinct file paths that have findings.
func (s *Store) FilesWithFindings(ctx context.Context, auditID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT file FROM findings WHERE audit_id = ? ORDER BY file`, auditID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RecordScan notes that a scan ran, so the UI can show scan history.
func (s *Store) RecordScan(ctx context.Context, auditID, scanID, engine string, count int, started, ended time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO scans (audit_id, scan_id, engine, started_at, ended_at, finding_count)
VALUES (?,?,?,?,?,?)
ON CONFLICT(audit_id, scan_id) DO UPDATE SET
    ended_at=excluded.ended_at, finding_count=excluded.finding_count`,
		auditID, scanID, engine,
		started.UTC().Format(time.RFC3339Nano), ended.UTC().Format(time.RFC3339Nano), count)
	return err
}

const findingColumns = `id, rule, tool, cwe, severity, confidence, file,
    start_line, end_line, code, message,
    evidence_source, evidence_sink, evidence_flow, evidence_note,
    remediation, patch, reachability, state, suppressed_until,
    pkg_purl, verified, verifier_note`

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func toAnySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

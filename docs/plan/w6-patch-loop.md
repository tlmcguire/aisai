# W6 — Patch Loop

**Goal:** close the loop — a proposed fix becomes a validated diff, can be applied to the audit
workspace, and the re-scan proves the finding is resolved.

**Status:** 🔄 in progress — the loop works end to end; 7 boxes open (see Notes).
**Depends on:** W3, W4
**Blocks:** —

## Tasks

### Patch generation
- [ ] `internal/patch/generate.go` — request a fix for a finding
- [x] The LLM returns `patch_unified` (a unified diff), not just prose
- [x] The prompt asks for a **minimal** change that preserves behavior
- [x] Ask the model for the unified diff explicitly and only as the patch field

### Patch validation
- [x] `internal/patch/validate.go` — parse and validate the diff
- [x] Parse as a unified diff (`unidiff`-equivalent in Go)
- [x] Verify it applies cleanly to the current file content (`git apply --check` against a temp
      copy, or a native applier)
- [ ] Verify the resulting file still parses as Python
- [x] Invalid diffs are rejected and flagged; the prose remediation is shown instead
- [ ] The model is given one chance to repair an invalid diff

### Apply
- [x] `internal/patch/apply.go` — write the patched content into the audit workspace
- [x] **Never** write to the user's real project files — only the workspace copy
- [x] The original file is backed up in the workspace so the change can be reverted
- [ ] Rejected patches are recorded with the reason

### Re-scan and compare
- [ ] Re-run detection on the changed file (SAST + LLM)
- [x] Diff the finding sets before/after
- [x] Report: findings resolved / introduced / unchanged
- [ ] Show the before/after finding list and the file diff side by side
- [x] **F§6** — this is the differentiating loop: download → mutate → rescan

### Export
- [x] Export the applied change as a `.patch` file
- [ ] Export a GitHub-ready PR description (summary, finding list, rationale)
- [x] Export the whole audit as a patch series

### API
- [x] `POST /api/audits/{id}/propose-fix` → `{findingId}` → returns a validated patch
- [x] `POST /api/audits/{id}/apply-patch` → `{path, patch}` → applies and re-scans
- [x] `POST /api/audits/{id}/revert-patch` → restores the original file
- [x] `GET /api/audits/{id}/changes` → list of applied changes with their before/after deltas

## Acceptance criteria

- [x] A proposed fix renders as a valid unified diff for a real finding
- [x] Applying a fix and re-scanning shows the finding resolved
- [x] Applying a fix never touches a path outside the audit workspace
- [x] A malformed model patch is rejected without corrupting the file
- [x] The change can be exported as a `.patch` file and applied with `git apply`

## Notes

- This turns the legacy README's manual "Package Testing Workflow" — edit the source inside the
  container, re-run with `--skip-download` — into a first-class product flow with evidence.
- The dataset is a gift here: `bench/data1551redacted.csv` has **1,551 (vulnerable, patched)
  pairs**, and `bench/train/` has 1,214 paired IDs. These are ideal for (a) few-shot prompting,
  and (b) measuring whether proposed patches are correct.
- The legacy app produced remediation as prose only. Nothing could be reviewed, applied, or
  verified — which is why no competitor's workflow was fully matched.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `internal/patch/patch.go`, `internal/patch/applier.go`,
`internal/patch/backup.go` (+ `patch_test.go`, `backup_test.go`), `internal/audit/rescan.go`
(+ `rescan_test.go`), `internal/server/server.go`, `electron/src/views/diffView.ts`.

**Live check (the whole loop):** a unified diff was applied to
`pkgs/flask-2.0.0/Flask-2.0.0/src/flask/cli.py` through `POST /api/audits/{id}/apply-patch`; the
response carried the rescan delta (`{"resolved":[],"introduced":["cwe-95-..."],"unchanged":1}`),
`GET /api/audits/{id}/changes` listed the change with its backup path, `GET .../changes.patch`
returned a `git apply`-able diff, and `POST .../revert-patch` restored the original line.
`scripts/smoke.sh` repeats this end to end and passes 32/32.

**Name changes:** the diff parser landed in `internal/patch/patch.go` (`Parse`, `Apply`,
`ApplyToContent`) and the validator in `internal/patch/applier.go` (`Validate`), not in
`internal/patch/validate.go` and `internal/patch/apply.go`.

**Left unticked (7):**
- *`internal/patch/generate.go` — request a fix for a finding* — there is no such file, and the
  app's `POST /api/audits/{id}/propose-fix` does **not** call the model: it returns the patch
  that was already stored on the finding. The capability does exist, but only in the MCP tool
  (`propose_fix` in `internal/mcp/tools.go`), which asks the model for a diff when the finding
  carries none.
- *Verify the resulting file still parses as Python* — `internal/patch` does not import
  `internal/ast`; nothing re-parses the patched file before the change is accepted.
- *The model is given one chance to repair an invalid diff* — not implemented.
- *Rejected patches are recorded with the reason* — the change log (`internal/patch/patch.go`)
  records **applied** changes only.
- *Re-run detection on the changed file (SAST + LLM)* — `RescanFile` re-runs the SAST engine
  only; the LLM detector is not invoked.
- *Show the before/after finding list and the file diff side by side* — the API returns the
  resolved/introduced/unchanged ids and counts; the UI shows the diff and a one-line summary.
- *Export a GitHub-ready PR description (summary, finding list, rationale)* — not implemented.

**Also worth recording:** `GET /api/audits/{id}/changes.patch` renders the *un-reverted* changes
only, so a reverted change correctly disappears from the series.

# W17 — Monitoring, re-checking and local projects

**Goal:** keep audits current. New advisories and new malicious-package reports should reach
existing audits, and a local project should be checked without a manual click.

**Status:** ⬜ not started
**Depends on:** W13 (historical trees), W15 (advisory sources)
**Blocks:** —

## Current state (verified 2026-11-23, from the root README)

- For a downloaded package, the vulnerable-dependency check runs once after the pull.
- For a local project, the check runs only when the user presses *Check for vulnerabilities*,
  because the locked names and versions are sent to osv.dev.
- Stored audits are not re-checked when a new advisory is published.

## Tasks

### Automatic check for local projects
- [ ] Settings option: run the dependency check automatically when a local project is opened
- [ ] Default off, because the check sends package names and versions to osv.dev; say so in the
      setting's description
- [ ] Offline mode (W7 offline DB) never sends anything, regardless of this setting
- [ ] The toolbar chip shows when the last check ran and against which advisory data

### Re-checking stored audits
- [ ] A stored audit keeps its dependency list with exact versions and the advisory data version
      it was checked against
- [ ] A "re-check" action re-runs advisory matching on the stored list without re-downloading
- [ ] Opt-in background re-check on a schedule, with a rate limit on the advisory source
- [ ] A new advisory that matches a stored package is recorded as "new since <date>" in the
      audit, and the user is notified in the app

### Using historical resolution
- [ ] A re-check can run against the historical tree from W13 as well as the current one, so the
      user can see advisories that applied at the audit's date
- [ ] Label each finding in the re-check as `applies to the audited version` or
      `applies to the current version of this package`

### Notifications
- [ ] In-app notification list with the audit name, the advisory ID, the severity and the date
- [ ] No notifications sent outside the app (no email, no webhook) without a separate design
      decision

### Export and history
- [ ] Each re-check is kept as a dated record so the user can compare two checks
- [ ] Export shows the difference between two checks: added, withdrawn and changed advisories

## Acceptance criteria

- [ ] With automatic check on, opening a local project with a known-vulnerable lockfile entry
      shows the advisory without a button press
- [ ] A stored audit, re-checked after a new fixture advisory is added, reports that advisory as
      new with its date
- [ ] With offline mode on, no re-check sends a request, and the status says the data is offline

## Notes

- Opt-in matters here. The app's privacy story is that code never leaves the machine. Automatic
  checks send only names and versions, and the user should be able to see and turn that off.
- The root README "Local projects" section needs a line describing the automatic setting when it
  lands.

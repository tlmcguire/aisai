# W12 — Malicious-package signals

**Goal:** flag packages that are malicious or suspicious, not only packages with known bugs.
OSV covers some known-malicious packages (`MAL-` IDs), but nothing here looks at install-time
behaviour, publishing history or whether a published artifact matches its source.

**Status:** ⬜ not started
**Depends on:** W1 (acquisition), W7 (advisory layer)
**Blocks:** —

## Current state (verified 2026-11-23)

- No code references `postinstall`, `setup.py` install hooks, `build.rs`, `.pth` files,
  maintainers, Scorecard, attestations, provenance or typosquatting.
- `internal/registry/crates.go` reads the `yanked` flag only to skip yanked versions. It does not
  report them.
- The W7 plan mentions surfacing `MAL-` advisories. That work is not done.

## Tasks

### Install-time code execution
- [ ] PyPI: flag `setup.py` that runs code at build time, `pyproject.toml` build backends that
      are not standard, and `.pth` files in the wheel or sdist
- [ ] npm: flag `preinstall`, `install`, `postinstall` and `prepare` scripts in `package.json`,
      and `bin` entries that download or execute remote content
- [ ] Cargo: flag `build.rs` and proc-macro crates, and report what they execute or fetch
- [ ] Each flag records the exact file and line, and is reported as a finding on the package, not
      as a vulnerability
- [ ] Tests with fixtures for each ecosystem, including a benign build script (must not be
      flagged as malicious, only noted)

### Obfuscation and exfiltration patterns
- [ ] Add rules for base64 or hex blobs passed to `exec`/`eval`/`Function`, and for `curl | sh`
      style downloads inside install scripts
- [ ] Add rules for reading environment variables or credential files and sending them to a
      network call inside an install hook
- [ ] Use the existing taint engine (`internal/sast/taint.go`) where it applies, and note where it
      cannot follow the flow

### Publishing and provenance
- [ ] Record the first-release date and the latest-release date; flag a package whose first
      release is very recent and that already has a large dependent set (possible name takeover)
- [ ] Record maintainer and publisher identities from registry metadata; flag a change of
      publisher between the previous and the audited version
- [ ] Record whether the release has a PyPI attestation or an npm provenance statement; report
      "absent" as information only
- [ ] Record the yanked status of the audited version and of the version the lockfile pins; flag
      an installed yanked version

### Source-to-artifact comparison
- [ ] Given a repository link in the package metadata, fetch the tag for the audited version
      (through the existing `internal/gitsrc` clone, with its restrictions)
- [ ] Diff the published artifact against the tagged source; report files present only in the
      artifact, and changed files that are not in the source
- [ ] Ignore expected build outputs (`*.egg-info`, generated `*_pb2.py`, minified bundles) using a
      named allowlist, and record the allowlist in the report

### Typosquatting and name checks
- [ ] Compare the audited name against the top N packages in each registry (keep the list in the
      repo with its date and source), and flag near-matches by edit distance and by normalized
      name (`-`, `_`, case)
- [ ] Report a near-match as a signal to check, never as a confirmed attack

### Scorecard and deps.dev signals
- [ ] Read the OpenSSF Scorecard from deps.dev when present (W7 enrichment item) and show the
      score with its check breakdown
- [ ] Do not turn a low Scorecard into a finding by itself

### Presentation
- [ ] `MAL-` advisories appear at the top of the advisory list with their own label
- [ ] The UI separates three kinds of risk: known CVE, malicious-package signal, and code finding.
      They must not be merged into one severity
- [ ] Reports and SARIF carry each kind with its own rule ID

## Acceptance criteria

- [ ] An npm fixture with a `postinstall` that fetches and runs remote code is flagged with the
      file and line
- [ ] A PyPI fixture whose sdist contains a file missing from its tagged source is reported by the
      source-to-artifact comparison
- [ ] A benign fixture with a build script produces a note, not a malicious finding
- [ ] Every signal states its evidence and what would make it a false positive

## Notes

- The highest-value items are install-time execution and artifact-versus-source comparison. They
  catch the attacks that CVE data misses, such as the xz-style tampering.
- Keep the wording of these signals cautious. A typosquat or a new publisher is a reason to look,
  and the app should say so.

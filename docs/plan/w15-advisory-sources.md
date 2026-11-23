# W15 — Advisory and risk sources

**Goal:** match vulnerabilities from more than one database, and rank them by how likely they are
to matter, not only by their severity label.

**Status:** ⬜ not started
**Depends on:** W7 (OSV layer)
**Blocks:** W11 (symbol data), W12 (malicious-package records)

## Current state (verified 2026-11-23)

- OSV is the only advisory source. The root README says so.
- Severity comes from OSV, normalised per `internal/advisory/status.go` and `osv.go`.
- There is no exploit-likelihood or exploited-in-the-wild signal.

## Tasks

### More sources
- [ ] GitHub Security Advisories (GHSA) through its API or the OSV mirror, deduplicated against
      OSV by alias (CVE and GHSA IDs)
- [ ] NVD (CVE records) for CVSS vectors and CWE when OSV lacks them; respect NVD rate limits and
      cache the responses
- [ ] PyPI per-version `vulnerabilities` field (already in the W7 plan, still open)
- [ ] Go vulnerability database for Go modules, once W16 adds Go as a package source
- [ ] Each source keeps its own record and its own timestamp; the normalised record links them

### Deduplication
- [ ] Merge records that share a CVE or GHSA alias into one advisory with a list of sources
- [ ] Keep the most specific affected-version range when sources disagree, and show the others

### Prioritisation signals
- [ ] EPSS: fetch the daily EPSS score for each CVE and show it with its date
- [ ] CISA KEV: flag CVEs listed as known exploited, and show the listing date
- [ ] Show these as separate signals next to severity; do not fold them into a single score
- [ ] Offline mode: ship a dated EPSS and KEV snapshot as an optional download, following the OSV
      offline pattern

### Records that are not vulnerabilities
- [ ] Map `MAL-` records to the malicious-package kind (W12) rather than the CVE list
- [ ] Map withdrawn advisories (OSV `withdrawn` field) to a hidden-by-default state, with a toggle

### VEX output
- [ ] CycloneDX 1.6 VEX section for each vulnerability: state (`exploitable`,
      `not_affected`, `in_triage`, `resolved`), justification where it is known (from W11), and
      the source of each claim
- [ ] Validate output against the CycloneDX 1.6 schema in CI (this is the W7 acceptance box,
      listed here because VEX depends on it)

### Freshness
- [ ] Record when each source was last synced; show a warning in the status bar when a source is
      older than its expected refresh interval

## Acceptance criteria

- [ ] A CVE that appears in both OSV and GHSA is shown once, with both sources listed
- [ ] EPSS and KEV values appear next to severity, with their dates
- [ ] With the network off and the snapshots installed, EPSS and KEV still show values and their
      dates
- [ ] A withdrawn advisory is hidden by default and visible when the toggle is on

## Notes

- Severity alone ranks poorly: many high-severity CVEs are never exploited, and some medium ones
  are. EPSS and KEV are the usual fix and are cheap to add once the source layer is generic.
- The VEX output here depends on the reachability work in W11 for its justifications. Until W11
  lands, justifications stay empty and the state stays `in_triage`.

package audit

import (
	"context"
	"errors"
	"sort"

	"github.com/tlmcguire/aisai/internal/advisory"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/registry"
)

// ErrNoAdvisorySource is returned when an advisory query is requested but no
// advisory client was configured.
var ErrNoAdvisorySource = errors.New("no advisory source configured")

// AdvisoryMode returns the mode the advisory source is running in.
func (m *Manager) AdvisoryMode() string {
	if m.deps.Advisory == nil {
		return "unavailable"
	}
	if m.deps.Advisory.Offline() {
		return "offline"
	}
	return "online"
}

// AdvisoryStatus reports the state of the advisory source.
func (m *Manager) AdvisoryStatus() advisory.Status {
	if m.deps.Advisory == nil {
		return advisory.Status{Mode: "unavailable", Available: false}
	}
	return m.deps.Advisory.Status()
}

// FetchAdvisories queries the advisory source for every package in the audit
// and records the results so the dependency graph can show vulnerability
// counts and severities.
//
// The results are cached per audit: advisories for a given package version do
// not change while an audit is open, and a graph with forty packages would
// otherwise mean forty identical round trips every time the graph is
// rendered. Pass refresh to force a re-query.
//
// A failure to reach the advisory source for one package does not discard the
// results for the others; the error is reported per package so the UI can
// show that the count is partial rather than silently reporting zero.
func (m *Manager) FetchAdvisories(ctx context.Context, id string, refresh bool) ([]model.Advisory, error) {
	a, err := m.audit(id)
	if err != nil {
		return nil, err
	}
	// A local project is not a published package: the advisory source would
	// look its folder name up as a package name and report the advisories of
	// an unrelated project.
	if a.source == sourceLocal {
		return []model.Advisory{}, nil
	}
	if m.deps.Advisory == nil {
		return nil, ErrNoAdvisorySource
	}

	a.mu.RLock()
	cached := a.advisoriesFetched && !refresh
	list := a.advisories
	a.mu.RUnlock()
	if cached {
		return list, nil
	}

	a.mu.RLock()
	pkgs := append([]model.Package(nil), a.packages...)
	a.mu.RUnlock()

	if len(pkgs) == 0 {
		return nil, errors.New("audit has no resolved packages")
	}

	byPkg := make(map[string][]model.Advisory, len(pkgs))
	seen := map[string]bool{}
	var all []model.Advisory

	for _, p := range pkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		advs, qerr := m.deps.Advisory.QueryFor(ctx, registry.OSVEcosystem(p.Ecosystem), p.Name, p.Version)
		if qerr != nil {
			// Keep going: one unreachable package must not lose the rest.
			m.emit(model.Event{
				Type:    model.EventAuditProgress,
				AuditID: id,
				Phase:   "advisories",
				Message: "Advisory lookup failed for " + p.Name + ": " + qerr.Error(),
			})
			continue
		}

		// OSV can return several records for the same vulnerability — a
		// GHSA record and a PYSEC record, say, both aliased to the same
		// CVE. The advisory id is the CVE when there is one, so those
		// records collapse to one advisory. They have to collapse per
		// package too, or the graph's vulnerability count would count the
		// duplicates while the advisory list showed one entry.
		uniq := make([]model.Advisory, 0, len(advs))
		pkgSeen := map[string]bool{}
		for _, adv := range advs {
			if pkgSeen[adv.ID] {
				continue
			}
			pkgSeen[adv.ID] = true
			// Record which package the advisory affects, so the SBOM
			// exports can attribute it.
			adv.Package = p.PURL
			uniq = append(uniq, adv)
			if seen[adv.ID] {
				continue
			}
			seen[adv.ID] = true
			all = append(all, adv)
		}
		byPkg[string(p.PURL)] = uniq
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Severity != all[j].Severity {
			return model.MaxSeverity(all[i].Severity, all[j].Severity) == all[i].Severity
		}
		return all[i].ID < all[j].ID
	})

	a.mu.Lock()
	for i := range a.packages {
		key := string(a.packages[i].PURL)
		advs, ok := byPkg[key]
		if !ok {
			continue
		}
		if len(advs) > 0 {
			a.packages[i].Vulnerabilities = advs
			max := a.packages[i].MaxSeverity
			for _, adv := range advs {
				max = model.MaxSeverity(max, adv.Severity)
			}
			a.packages[i].MaxSeverity = max
		}
	}
	a.advisories = all
	a.advisoriesByPkg = byPkg
	a.advisoriesFetched = true
	a.mu.Unlock()

	return all, nil
}

// Advisories returns the advisories already fetched for an audit, without
// querying the source. It returns an error for an unknown audit and nil when
// no fetch has happened yet.
func (m *Manager) Advisories(id string) ([]model.Advisory, error) {
	a, err := m.audit(id)
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := append([]model.Advisory(nil), a.advisories...)
	for i := range out {
		if out[i].Package == "" {
			for purl, advs := range a.advisoriesByPkg {
				for _, adv := range advs {
					if adv.ID == out[i].ID {
						out[i].Package = model.PURL(purl)
						break
					}
				}
				if out[i].Package != "" {
					break
				}
			}
		}
	}
	return out, nil
}

// AdvisoriesForPackage returns the advisories that affect one package of an
// audit, keyed by the package's PURL.
func (m *Manager) AdvisoriesForPackage(id, purl string) []model.Advisory {
	a, err := m.audit(id)
	if err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]model.Advisory(nil), a.advisoriesByPkg[purl]...)
}

// audit looks up an audit by id.
func (m *Manager) audit(id string) (*Audit, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}
	return a, nil
}

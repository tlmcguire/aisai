package audit

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/tlmcguire/aisai/internal/advisory"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/purl"
	"github.com/tlmcguire/aisai/internal/registry"
)

// advisoryWorkers is how many advisory lookups run at the same time. OSV is a
// public service, so the number is kept modest.
const advisoryWorkers = 8

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

	a.mu.Lock()
	a.advisoriesRunning = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.advisoriesRunning = false
		a.mu.Unlock()
	}()

	a.mu.RLock()
	pkgs := append([]model.Package(nil), a.packages...)
	edges := append([]model.GraphEdge(nil), a.edges...)
	rootName := purl.NormalizeName(a.Package)
	local := a.source == sourceLocal
	a.mu.RUnlock()

	if len(pkgs) == 0 {
		return nil, errors.New("audit has no resolved packages")
	}
	names := make(map[model.PURL]string, len(pkgs))
	var rootPURL model.PURL
	for _, p := range pkgs {
		names[p.PURL] = p.Name
		if rootPURL == "" && (p.Ecosystem == "local" || purl.NormalizeName(p.Name) == rootName) {
			rootPURL = p.PURL
		}
	}

	byPkg := make(map[string][]model.Advisory, len(pkgs))
	seen := map[string]bool{}
	var all []model.Advisory
	checked := 0

	// The lookups are independent, so they run side by side: a project with
	// hundreds of locked packages took about a minute one at a time. The
	// results are used in package order afterwards, so the output does not
	// depend on which answer came back first.
	type lookup struct {
		advs []model.Advisory
		err  error
		done bool
	}
	results := make([]lookup, len(pkgs))
	var wg sync.WaitGroup
	slots := make(chan struct{}, advisoryWorkers)
	for i, p := range pkgs {
		// A local project is not a published package, so it is not looked up
		// under its folder name (that would report the advisories of an
		// unrelated project). Its locked dependencies are: they are real
		// packages with exact versions. A package that comes from git or a
		// path, or whose version is only a range, cannot be matched.
		if local && !lookupable(p) {
			continue
		}
		checked++
		wg.Add(1)
		slots <- struct{}{}
		go func(i int, p model.Package) {
			defer wg.Done()
			defer func() { <-slots }()
			advs, qerr := m.deps.Advisory.QueryFor(ctx, registry.OSVEcosystem(p.Ecosystem), p.Name, p.Version)
			results[i] = lookup{advs: advs, err: qerr, done: true}
		}(i, p)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for i, p := range pkgs {
		if !results[i].done {
			continue
		}
		advs, qerr := results[i].advs, results[i].err
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
			adv.Via = chainTo(edges, names, rootPURL, p.PURL)
			uniq = append(uniq, adv)
			// One entry per advisory and package: the same vulnerability in two
			// packages is two things to update, and each has its own chain.
			key := adv.ID + "|" + string(p.PURL)
			if seen[key] {
				continue
			}
			seen[key] = true
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
	a.advisoriesChecked = checked
	a.mu.Unlock()

	return all, nil
}

// AdvisoriesRunning reports whether an advisory lookup is in flight for an audit.
func (m *Manager) AdvisoriesRunning(id string) bool {
	a, err := m.audit(id)
	if err != nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.advisoriesRunning
}

// AdvisoryState reports whether the advisories of an audit were looked up
// and how many packages were checked, so that "none found" can be told from
// "not checked yet".
func (m *Manager) AdvisoryState(id string) (fetched bool, checked int, err error) {
	a, err := m.audit(id)
	if err != nil {
		return false, 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.advisoriesFetched, a.advisoriesChecked, nil
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

// lookupable reports whether a locked package can be looked up in an advisory
// database: it comes from a registry, has an exact version, and is in an
// ecosystem OSV knows.
func lookupable(p model.Package) bool {
	if p.Ecosystem == "local" || p.Ecosystem == "" || p.Source == "git" || p.Source == "path" {
		return false
	}
	v := strings.TrimPrefix(p.Version, "v")
	if v == "" || v == "*" || strings.ContainsAny(v, "<>=~^*, ") {
		return false
	}
	return true
}

// chainTo returns the shortest chain of package names from the root to a
// package, the root itself left out: ["express", "qs"] means the project
// depends on express, which depends on qs. A package the root reaches
// directly gives a chain of one. It returns nil when no path is known.
func chainTo(edges []model.GraphEdge, names map[model.PURL]string, root, target model.PURL) []string {
	if root == "" || target == "" || root == target {
		return nil
	}
	next := map[model.PURL][]model.PURL{}
	for _, e := range edges {
		next[e.Source] = append(next[e.Source], e.Target)
	}
	parent := map[model.PURL]model.PURL{root: ""}
	queue := []model.PURL{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			break
		}
		for _, to := range next[cur] {
			if _, seen := parent[to]; seen {
				continue
			}
			parent[to] = cur
			queue = append(queue, to)
		}
	}
	if _, ok := parent[target]; !ok {
		return nil
	}
	var rev []string
	for cur := target; cur != "" && cur != root; cur = parent[cur] {
		rev = append(rev, names[cur])
	}
	chain := make([]string, len(rev))
	for i, n := range rev {
		chain[len(rev)-1-i] = n
	}
	return chain
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

// transitiveVulnCounts returns, for every package, how many of the vulnerable
// packages are somewhere below it. It walks the edges backwards from each
// vulnerable package once, so its cost grows with the vulnerable packages and
// their ancestors, not with the size of the whole graph squared.
func transitiveVulnCounts(edges []model.GraphEdge, vulnerable []model.PURL) map[model.PURL]int {
	parents := map[model.PURL][]model.PURL{}
	for _, e := range edges {
		parents[e.Target] = append(parents[e.Target], e.Source)
	}
	counts := map[model.PURL]int{}
	for _, v := range vulnerable {
		seen := map[model.PURL]bool{v: true}
		stack := []model.PURL{v}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, p := range parents[cur] {
				if seen[p] {
					continue
				}
				seen[p] = true
				counts[p]++
				stack = append(stack, p)
			}
		}
	}
	return counts
}

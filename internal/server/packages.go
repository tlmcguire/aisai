package server

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/pypi"
)

// maxPackageVersions is the number of versions a lookup reports.
//
// A long-lived project can have hundreds of releases. The picker only ever
// offers the recent ones, and the index stays responsive.
const maxPackageVersions = 40

// handlePackageLookup resolves a package name on the package index.
//
// The name the user typed is turned into the project it refers to, together
// with its summary and the versions that can be audited. A name that does
// not exist is reported in the error field of the response rather than as an
// HTTP failure: the lookup is a question, not a fetch of a known resource.
func (s *Server) handlePackageLookup(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "a package name is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"package": s.packageLookup(r.Context(), name),
	})
}

// packageLookup resolves a package name and lists its versions.
func (s *Server) packageLookup(ctx context.Context, name string) *model.PackageLookup {
	out := &model.PackageLookup{Name: name}
	if s.pypi == nil {
		out.Error = "the package index is not available"
		return out
	}
	info, err := s.pypi.Package(ctx, name, "")
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Name = info.Name
	out.Summary = info.Summary
	out.Latest = info.Version

	versions, err := s.pypi.Releases(ctx, info.Name)
	if err == nil {
		out.Versions = newestVersions(versions, maxPackageVersions)
	}
	return out
}

// newestVersions returns the newest versions of a list, newest first.
//
// Pre-releases are kept but sorted after the stable versions of the same
// number, because a pre-release is rarely what someone means by "the
// version of this package".
func newestVersions(versions []string, limit int) []string {
	if len(versions) == 0 {
		return nil
	}
	sorted := append([]string(nil), versions...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if pypi.IsPrerelease(a) != pypi.IsPrerelease(b) {
			return !pypi.IsPrerelease(a)
		}
		return pypi.CompareVersions(a, b) > 0
	})
	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

// handleDeleteWorkspace removes the downloaded sources of an audit.
//
// The findings, the graph and the report of the audit are kept: only the
// sandbox of downloaded code is removed, so the disk space is freed without
// losing the result of the inspection.
func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no audit manager is configured")
		return
	}
	freed, err := s.manager.DeleteSources(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	audit, err := s.manager.Snapshot(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"audit":     audit,
		"freed":     freed,
		"freedText": humanBytes(freed),
	})
}

// humanBytes renders a byte count the way the UI shows it.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + units[exp]
}

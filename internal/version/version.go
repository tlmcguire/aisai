// Package version exposes the build-stamped application version.
//
// The version is stamped at build time with -ldflags:
//
//	go build -ldflags "\
//	  -X github.com/tlmcguire/aisai/internal/version.Version=1.2.3 \
//	  -X github.com/tlmcguire/aisai/internal/version.Commit=$(git rev-parse --short HEAD) \
//	  -X github.com/tlmcguire/aisai/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
//	  ./cmd/aisai-server
//
// When a field was not stamped, it is filled in from the build information
// that the Go toolchain embeds in every binary (runtime/debug.ReadBuildInfo):
// the module version when the binary was built from a tagged module, and the
// VCS revision and commit time when it was built from a checkout. A field
// that was stamped explicitly always wins.
package version

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// The placeholder values. A field still holding its placeholder at init time
// is considered "not stamped" and is resolved from the build information.
const (
	// DefaultVersion is the version of an unstamped build.
	DefaultVersion = "0.3.0-dev"
	// UnknownCommit is the commit of an unstamped build.
	UnknownCommit = "unknown"
	// UnknownDate is the build date of an unstamped build.
	UnknownDate = "unknown"
)

// These are overridden at build time with -ldflags.
var (
	// Version is the semantic version of the build.
	Version = DefaultVersion
	// Commit is the git commit the build was made from.
	Commit = UnknownCommit
	// Date is the build timestamp.
	Date = UnknownDate
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		applyBuildInfo(info)
	}
}

// applyBuildInfo fills in the fields that were not stamped with -ldflags.
//
// A stamped field is never overwritten: the explicit -ldflags value is the
// authority, and this only supplies what the build left out.
func applyBuildInfo(info *debug.BuildInfo) {
	if info == nil {
		return
	}
	if Version == DefaultVersion {
		if v := moduleVersion(info); v != "" {
			Version = v
		}
	}
	if Commit == UnknownCommit {
		if rev := setting(info, "vcs.revision"); rev != "" {
			Commit = rev
			if setting(info, "vcs.modified") == "true" {
				Commit += "-dirty"
			}
		}
	}
	if Date == UnknownDate {
		if t := setting(info, "vcs.time"); t != "" {
			Date = t
		}
	}
}

// moduleVersion returns the version of the module the binary was built from.
//
// The Go toolchain reports "(devel)" for a binary built from a checkout, and
// the real version for a binary built from a tagged module ("go install
// example.com/mod@v1.2.3"). Only the latter is a usable version, so the
// placeholder is reported as "not stamped".
func moduleVersion(info *debug.BuildInfo) string {
	v := strings.TrimSpace(info.Main.Version)
	if v == "" || v == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

// setting returns a build setting such as "vcs.revision" or "vcs.time".
func setting(info *debug.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return strings.TrimSpace(s.Value)
		}
	}
	return ""
}

// SetVersion overrides the version at runtime.
//
// It is used by the command line entry points that accept a --version flag
// and by tests. An empty value is ignored, so a caller cannot accidentally
// blank the version.
func SetVersion(v string) {
	if v = strings.TrimSpace(v); v != "" {
		Version = v
	}
}

// SetBuild overrides the commit and the build date at runtime.
func SetBuild(commit, date string) {
	if commit = strings.TrimSpace(commit); commit != "" {
		Commit = commit
	}
	if date = strings.TrimSpace(date); date != "" {
		Date = date
	}
}

// String renders the full version string.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}

// Short returns just the version number.
func Short() string { return Version }

// Flag registers the -version flag on fs.
//
// The flag is a boolean, so "--version" needs no argument. The flag is not
// acted upon here: a command that supports it should call HandleVersion
// after parsing, so that the flag works with any flag set and not only with
// the default one.
func Flag(fs *flag.FlagSet) *bool {
	return fs.Bool("version", false, "print the version and exit")
}

// HandleVersion prints the version to stdout and returns true when the
// -version flag was set.
//
// A command wires it up like this:
//
//	if version.HandleVersion(flag.CommandLine) {
//	    return
//	}
func HandleVersion(fs *flag.FlagSet) bool {
	f := fs.Lookup("version")
	if f == nil {
		return false
	}
	if f.Value.String() != "true" {
		return false
	}
	_, _ = fmt.Fprintln(os.Stdout, String())
	return true
}

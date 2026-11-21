package version

import (
	"flag"
	"runtime/debug"
	"strings"
	"testing"
)

func TestApplyBuildInfoFillsUnstampedFields(t *testing.T) {
	Version = DefaultVersion
	Commit = UnknownCommit
	Date = UnknownDate

	info := &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/tlmcguire/aisai", Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-11-21T10:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	applyBuildInfo(info)

	if Commit != "0123456789abcdef" {
		t.Fatalf("commit = %q, want the vcs.revision setting", Commit)
	}
	if Date != "2026-11-21T10:00:00Z" {
		t.Fatalf("date = %q, want the vcs.time setting", Date)
	}
	// "(devel)" is not a version, so the placeholder must survive.
	if Version != DefaultVersion {
		t.Fatalf("version = %q, want the placeholder to survive", Version)
	}
}

func TestApplyBuildInfoKeepsStampedFields(t *testing.T) {
	Version = "1.2.3"
	Commit = "deadbeef"
	Date = "2026-01-01T00:00:00Z"

	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "cafebabe"},
			{Key: "vcs.time", Value: "2030-01-01T00:00:00Z"},
		},
	})

	if Version != "1.2.3" || Commit != "deadbeef" || Date != "2026-01-01T00:00:00Z" {
		t.Fatalf("stamped fields were overwritten: %q %q %q", Version, Commit, Date)
	}
}

func TestApplyBuildInfoUsesModuleVersion(t *testing.T) {
	Version = DefaultVersion
	Commit = UnknownCommit
	Date = UnknownDate

	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Path: "github.com/tlmcguire/aisai", Version: "v2.5.1"},
	})
	if Version != "2.5.1" {
		t.Fatalf("version = %q, want 2.5.1", Version)
	}
}

func TestApplyBuildInfoMarksDirtyCheckout(t *testing.T) {
	Version = DefaultVersion
	Commit = UnknownCommit
	Date = UnknownDate

	applyBuildInfo(&debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.modified", Value: "true"},
		},
	})
	if !strings.HasSuffix(Commit, "-dirty") {
		t.Fatalf("commit = %q, want a -dirty suffix", Commit)
	}
}

func TestApplyBuildInfoIgnoresNil(t *testing.T) {
	Version = DefaultVersion
	Commit = UnknownCommit
	Date = UnknownDate
	applyBuildInfo(nil)
	if Version != DefaultVersion || Commit != UnknownCommit || Date != UnknownDate {
		t.Fatal("a nil build info must not change anything")
	}
}

func TestSetVersionIgnoresEmpty(t *testing.T) {
	Version = "1.0.0"
	SetVersion("  ")
	if Version != "1.0.0" {
		t.Fatalf("version = %q, want it unchanged", Version)
	}
	SetVersion("2.0.0")
	if Version != "2.0.0" {
		t.Fatalf("version = %q, want 2.0.0", Version)
	}
}

func TestSetBuildIgnoresEmpty(t *testing.T) {
	Commit = "old"
	Date = "old"
	SetBuild("", "")
	if Commit != "old" || Date != "old" {
		t.Fatal("empty values must not overwrite the build metadata")
	}
	SetBuild("new", "2026-11-21")
	if Commit != "new" || Date != "2026-11-21" {
		t.Fatalf("commit = %q, date = %q", Commit, Date)
	}
}

func TestHandleVersionPrintsAndReports(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	Flag(fs)
	if HandleVersion(fs) {
		t.Fatal("HandleVersion must be false before the flag is set")
	}
	if err := fs.Parse([]string{"--version"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !HandleVersion(fs) {
		t.Fatal("HandleVersion must be true once the flag is set")
	}
}

func TestHandleVersionWithoutFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	if HandleVersion(fs) {
		t.Fatal("HandleVersion must be false when the flag was never registered")
	}
}

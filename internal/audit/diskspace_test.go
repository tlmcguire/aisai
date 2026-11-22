package audit

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

func TestExplainDiskFull(t *testing.T) {
	wrapped := fmt.Errorf("pypi: download x: %w", &errNo{syscall.ENOSPC})
	got := explainDiskFull(wrapped, t.TempDir())
	var full *DiskFullError
	if !errors.As(got, &full) || !full.Started {
		t.Fatalf("want a DiskFullError, got %v", got)
	}
	if !strings.Contains(got.Error(), "disk is full") {
		t.Fatalf("message: %s", got)
	}
	other := errors.New("boom")
	if explainDiskFull(other, "") != other {
		t.Fatal("other errors must pass through")
	}
}

func TestCheckDiskSpaceAllowsNormalDisk(t *testing.T) {
	if free, ok := freeSpace(t.TempDir()); ok && free < minFreeBytes {
		t.Skip("the test disk is nearly full")
	}
	if err := checkDiskSpace(t.TempDir() + "/not/yet/created"); err != nil {
		t.Fatal(err)
	}
}

// errNo wraps an Errno the way an *os.PathError does.
type errNo struct{ e syscall.Errno }

func (e *errNo) Error() string { return "write: " + e.e.Error() }
func (e *errNo) Unwrap() error { return e.e }

//go:build !windows

package audit

import (
	"errors"
	"syscall"
)

// freeSpace reports the bytes an unprivileged process can still write to the
// disk that holds path. statfs(2) fills in the block size and the number of
// blocks available to non-root users (Bavail, not Bfree, which counts the
// blocks reserved for root): https://man7.org/linux/man-pages/man2/statfs.2.html
func freeSpace(path string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), true //nolint:gosec // block counts are not negative
}

// isNoSpace reports whether err is the operating system saying the disk or the
// user's quota is full.
func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

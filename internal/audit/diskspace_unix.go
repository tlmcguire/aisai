//go:build !windows

package audit

import (
	"errors"
	"syscall"
)

// freeSpace reports the bytes an unprivileged process can still write to the
// disk that holds path.
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

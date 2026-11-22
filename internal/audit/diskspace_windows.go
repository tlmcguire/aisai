//go:build windows

package audit

import (
	"errors"
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// freeSpace reports the bytes the current user can still write to the disk
// that holds path.
func freeSpace(path string) (uint64, bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var avail, total, free uint64
	r, _, _ := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if r == 0 {
		return 0, false
	}
	return avail, true
}

// isNoSpace reports whether err is Windows saying the disk is full.
func isNoSpace(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == 112 || errno == 39 // ERROR_DISK_FULL, ERROR_HANDLE_DISK_FULL
	}
	return false
}

//go:build windows

package diskspace

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// Available returns the number of bytes available to the current user on the
// volume containing path.
func Available(path string) (uint64, error) {
	existing, err := nearestExisting(path)
	if err != nil {
		return 0, err
	}
	ptr, err := syscall.UTF16PtrFromString(existing)
	if err != nil {
		return 0, err
	}
	var available uint64
	r1, _, callErr := getDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&available)),
		0,
		0,
	)
	if r1 == 0 {
		if callErr != syscall.Errno(0) {
			return 0, callErr
		}
		return 0, fmt.Errorf("GetDiskFreeSpaceExW failed")
	}
	return available, nil
}

func nearestExisting(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
		p = parent
	}
}

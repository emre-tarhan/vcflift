//go:build windows

package cachelock

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func acquire(cacheDir string) (func(), error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(cacheDir, ".lock")
	// Share mode 0 makes the open exclusive: a second opener receives a
	// sharing violation while the first handle is alive, which is the mutex.
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(p),
		windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok && errno == windows.ERROR_SHARING_VIOLATION {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { _ = windows.CloseHandle(h) }, nil
}

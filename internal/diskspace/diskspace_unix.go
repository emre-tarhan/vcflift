//go:build !windows

package diskspace

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Available returns the number of bytes available to an unprivileged process on
// the filesystem containing path. The nearest existing parent is used so callers
// can check a cache directory before it has been created.
func Available(path string) (uint64, error) {
	existing, err := nearestExisting(path)
	if err != nil {
		return 0, err
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(existing, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
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

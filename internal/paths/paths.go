package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

func CacheDir() string {
	if runtime.GOOS == "windows" {
		if p := os.Getenv("LOCALAPPDATA"); p != "" {
			return filepath.Join(p, "VCFLift")
		}
	}
	if p, err := os.UserCacheDir(); err == nil {
		return filepath.Join(p, "vcflift")
	}
	return filepath.Join(os.TempDir(), "vcflift")
}

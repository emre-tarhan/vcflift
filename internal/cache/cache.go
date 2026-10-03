package cache

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/cachelock"
)

type Status struct {
	Root           string `json:"root"`
	TotalBytes     int64  `json:"total_bytes"`
	ResourcesBytes int64  `json:"resources_bytes"`
	RuntimeBytes   int64  `json:"runtime_bytes"`
	EngineBytes    int64  `json:"engine_bytes"`
	TempBytes      int64  `json:"temp_bytes"`
}

type CleanupResult struct {
	RemovedFiles int   `json:"removed_files"`
	FreedBytes   int64 `json:"freed_bytes"`
}

func Inspect(root string) (Status, error) {
	st := Status{Root: root}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return st, nil
	} else if err != nil {
		return st, err
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size := info.Size()
		st.TotalBytes += size
		rel, _ := filepath.Rel(root, path)
		top := strings.Split(filepath.ToSlash(rel), "/")[0]
		switch top {
		case "resources":
			st.ResourcesBytes += size
		case "runtime":
			st.RuntimeBytes += size
		case "engine":
			st.EngineBytes += size
		}
		if isTemporary(path) {
			st.TempBytes += size
		}
		return nil
	})
	return st, err
}

// CleanSafe removes resumable partials that are no longer active, GATK download
// archives left after installation, and compressed reference archives only when
// their prepared FASTA + sidecars are already present. Prepared references,
// chains, aliases, engine payloads and installed runtimes are preserved.
func CleanSafe(root string) (CleanupResult, error) {
	var result CleanupResult
	release, err := cachelock.Acquire(root)
	if err != nil {
		return result, err
	}
	defer release()
	remove := func(path string) error {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		result.RemovedFiles++
		result.FreedBytes += info.Size()
		return nil
	}

	// Stale .part files are safe to remove from an explicit cleanup action.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".part") {
			_ = remove(path)
		}
		return nil
	})

	resRoot := filepath.Join(root, "resources", "v1")
	for _, base := range []string{"hg38.fa", "hg19.fa"} {
		prepared := filepath.Join(resRoot, base)
		if regular(prepared) && regular(prepared+".fai") && regular(dictPath(prepared)) {
			if err := remove(prepared + ".gz"); err != nil {
				return result, err
			}
		}
	}

	downloads := filepath.Join(root, "runtime", "v1", "downloads")
	entries, err := os.ReadDir(downloads)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if err := remove(filepath.Join(downloads, e.Name())); err != nil {
				return result, err
			}
		}
	} else if !os.IsNotExist(err) {
		return result, err
	}
	return result, nil
}

func ClearAll(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("cache root is empty")
	}
	clean := filepath.Clean(root)
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("refusing to remove unsafe cache root %q", root)
	}
	release, err := cachelock.Acquire(clean)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(clean)
	if err != nil {
		release()
		return err
	}
	// The lock file itself stays in place until the lock is released; deleting
	// it early would break exclusive open on Windows.
	for _, e := range entries {
		if e.Name() == ".lock" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(clean, e.Name())); err != nil {
			release()
			return err
		}
	}
	release()
	_ = os.Remove(filepath.Join(clean, ".lock"))
	return nil
}

func isTemporary(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.HasSuffix(lower, ".part") || strings.Contains(lower, "/runtime/v1/downloads/") || strings.HasSuffix(lower, ".fa.gz")
}

func regular(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func dictPath(fasta string) string {
	ext := filepath.Ext(fasta)
	if ext == "" {
		return fasta + ".dict"
	}
	return strings.TrimSuffix(fasta, ext) + ".dict"
}

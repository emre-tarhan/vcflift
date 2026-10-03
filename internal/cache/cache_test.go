package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanSafeKeepsPreparedReferences(t *testing.T) {
	root := t.TempDir()
	r := filepath.Join(root, "resources", "v1")
	if err := os.MkdirAll(r, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hg38.fa", "hg38.fa.fai", "hg38.dict", "hg38.fa.gz"} {
		if err := os.WriteFile(filepath.Join(r, name), []byte("1234"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "orphan.part"), []byte("xx"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := CleanSafe(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedFiles != 2 {
		t.Fatalf("removed=%d", result.RemovedFiles)
	}
	if _, err := os.Stat(filepath.Join(r, "hg38.fa")); err != nil {
		t.Fatal("prepared fasta removed")
	}
	if _, err := os.Stat(filepath.Join(r, "hg38.fa.gz")); !os.IsNotExist(err) {
		t.Fatal("archive not removed")
	}
}

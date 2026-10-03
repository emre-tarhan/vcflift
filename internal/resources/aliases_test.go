package resources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildRenameMap(t *testing.T) {
	d := t.TempDir()
	in := filepath.Join(d, "aliases.txt")
	out := filepath.Join(d, "rename.tsv")
	if err := os.WriteFile(in, []byte("chr1\t1\tNC_000001.11\nchrX\tX\tNC_000023.11\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := BuildRenameMap(in, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"1\tchr1", "NC_000001.11\tchr1", "X\tchrX"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
}

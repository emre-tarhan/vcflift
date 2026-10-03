package resources

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatusDistinguishesPreparedFASTAAndDownloadedFiles(t *testing.T) {
	d := t.TempDir()
	m := NewManager(d)
	manifest := Manifest{Resources: []Resource{
		{ID: "ref", Name: "Reference", Filename: "ref.fa.gz", PreparedFilename: "ref.fa", Transform: TransformGzipFASTA},
		{ID: "chain", Name: "Chain", Filename: "chain.gz", Transform: TransformNone},
	}}
	for _, name := range []string{"ref.fa", "ref.fa.fai", "ref.dict", "chain.gz"} {
		if err := os.WriteFile(filepath.Join(d, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := m.Status(manifest)
	if len(st) != 2 || !st[0].Ready || !st[1].Ready {
		t.Fatalf("unexpected statuses: %+v", st)
	}
	if !m.AllReady(manifest) {
		t.Fatal("expected all resources ready")
	}
	if err := os.Remove(filepath.Join(d, "ref.fa.fai")); err != nil {
		t.Fatal(err)
	}
	if m.AllReady(manifest) {
		t.Fatal("missing FASTA sidecar should make profile not ready")
	}
}

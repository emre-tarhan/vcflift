package resources

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareGzipFASTAAndFAI(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "tiny.fa.gz")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	_, _ = gz.Write([]byte(">chr1 description\nACGT\nAC\n>chr2\nTTTT\n"))
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(d, "tiny.fa")
	if err := prepareGzipFASTA(src, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst + ".fai")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "chr1\t6\t18\t4\t5") {
		t.Fatalf("unexpected fai: %q", text)
	}
	if !strings.Contains(text, "chr2\t4") {
		t.Fatalf("unexpected fai: %q", text)
	}

	dict, err := os.ReadFile(filepath.Join(d, "tiny.dict"))
	if err != nil {
		t.Fatal(err)
	}
	dictText := string(dict)
	if !strings.Contains(dictText, "@SQ\tSN:chr1\tLN:6") || !strings.Contains(dictText, "@SQ\tSN:chr2\tLN:4") {
		t.Fatalf("unexpected dict: %q", dictText)
	}
}

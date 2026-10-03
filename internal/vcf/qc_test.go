package vcf

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSummarizeSourceCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deepvariant.g.vcf.gz")
	writeQCFixture(t, path, "##fileformat=VCFv4.2\n"+
		"#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\tFORMAT\tS\n"+
		"chr1\t1\t.\tA\t<*>\t.\tRefCall\tEND=3\tGT:GQ\t0/0:50\n"+
		"chr1\t4\t.\tC\tT,<*>\t50\tPASS\t.\tGT:GQ\t0/1:50\n"+
		"chr1\t5\t.\tG\t<DEL>,<*>\t50\tPASS\t.\tGT:GQ\t0/1:50\n"+
		"chr1\t6\t.\tA\tAT,<*>\t50\tPASS\t.\tGT:GQ\t1/1:50\n"+
		"chr1\t7\t.\tATAT\tT,<*>\t50\tPASS\t.\tGT:GQ\t0/1:50\n"+
		"chr1\t8\t.\tA\tG,<*>\t.\tNoCall\t.\tGT:GQ\t./.:0\n")

	s, err := SummarizeSourceCalls(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Records != 6 || s.NonReferenceCalls != 4 || s.NonReferencePASS != 4 || s.LiftoverCandidates != 4 {
		t.Fatalf("summary=%+v", s)
	}
}

func TestSummarizeVariantFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rejects.vcf.gz")
	writeQCFixture(t, path, "##fileformat=VCFv4.2\n"+
		"#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n"+
		"chr1\t10\t.\tA\tT\t.\tUnmappedAnchors\t.\n"+
		"chr1\t20\t.\tA\t<NON_REF>\t.\tMismatchRef;UnmappedAnchors\t.\n"+
		"chr2\t30\t.\tC\t<*>\t.\tPASS\t.\n")

	s, err := SummarizeVariantFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Records != 3 || s.StarAlleleRecords != 1 || s.NonRefAlleleRecords != 1 {
		t.Fatalf("summary=%+v", s)
	}
	if s.FilterCounts["UnmappedAnchors"] != 1 || s.RejectReasonCounts["UnmappedAnchors"] != 2 || s.ContigCounts["chr1"] != 2 {
		t.Fatalf("distributions=%+v", s)
	}
}

func writeQCFixture(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCountFilterStream(t *testing.T) {
	input := "PASS\nMismatchAnchors\nPASS\n.\nUnmappedAnchors\n"
	records, pass, err := countFilterStream(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if records != 5 {
		t.Fatalf("records=%d want 5", records)
	}
	if pass != 2 {
		t.Fatalf("pass=%d want 2", pass)
	}
}

func TestSummarizeSourceCallsBCFTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake bcftools fixture is a POSIX shell script")
	}
	d := t.TempDir()
	fake := filepath.Join(d, "bcftools")
	script := `#!/bin/sh
case "$1" in
  index) exit 1 ;;
  view) printf 'PASS\nPASS\nLowQual\n'; exit 0 ;;
  query) cat; exit 0 ;;
esac
exit 9
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d, "source.vcf")
	body := "##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\tFORMAT\tS\n" +
		"chr1\t1\t.\tA\tT\t.\tPASS\t.\tGT\t0/1\n" +
		"chr1\t2\t.\tA\tT\t.\tPASS\t.\tGT\t0/1\n" +
		"chr1\t3\t.\tA\tT\t.\tLowQual\t.\tGT\t0/1\n" +
		"chr1\t4\t.\tA\t<*>\t.\tRefCall\tEND=5\tGT\t0/0\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := SummarizeSourceCallsBCFTools(context.Background(), fake, path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if s.Records != 4 || s.NonReferenceCalls != 3 || s.NonReferencePASS != 2 || s.LiftoverCandidates != 3 {
		t.Fatalf("summary=%+v", s)
	}
}

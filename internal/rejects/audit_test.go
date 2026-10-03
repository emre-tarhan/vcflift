package rejects

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyze(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.vcf")
	body := "##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nchr1\t1\t.\tA\tG\t.\tUnmappedAnchors\t.\nchr1\t2\t.\tA\tAT\t.\tMismatchAnchors\t.\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.Records != 2 || a.Types["SNP"] != 1 || a.Types["INDEL"] != 1 {
		t.Fatalf("audit=%+v", a)
	}
}

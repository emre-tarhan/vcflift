package vcf

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emre-tarhan/vcflift/internal/model"
)

func writeGzip(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.vcf.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInspectGATKGVCF(t *testing.T) {
	path := writeGzip(t, "##fileformat=VCFv4.2\n##reference=GRCh38\n##contig=<ID=chr1,length=248956422>\n##ALT=<ID=NON_REF,Description=\"Represents any possible alternative allele\">\n##INFO=<ID=END,Number=1,Type=Integer,Description=\"Stop position\">\n##GVCFBlock90-99=minGQ=90(inclusive),maxGQ=99(exclusive)\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\tFORMAT\tSAMPLE\nchr1\t100\t.\tA\t<NON_REF>\t.\t.\tEND=150\tGT:DP:GQ:MIN_DP:PL\t0/0:30:99:25:0,90,900\n")
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != model.FileKindGVCF {
		t.Fatalf("kind=%s", got.Kind)
	}
	if got.Assembly != model.AssemblyHG38 {
		t.Fatalf("assembly=%s", got.Assembly)
	}
	if got.ContigStyle != model.ContigStyleUCSC {
		t.Fatalf("contig style=%s", got.ContigStyle)
	}
	if !got.HasReferenceBlock {
		t.Fatal("reference block not detected")
	}
	if len(got.Samples) != 1 || got.Samples[0] != "SAMPLE" {
		t.Fatalf("samples=%v", got.Samples)
	}
}

func TestInspectDeepVariantGVCF(t *testing.T) {
	path := writeGzip(t, "##fileformat=VCFv4.2\n##DeepVariant_version=1.10.0\n##contig=<ID=chr1,length=248956422>\n##INFO=<ID=END,Number=1,Type=Integer,Description=\"End position\">\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\tFORMAT\tSAMPLE\nchr1\t10001\t.\tA\t<*>\t.\t.\tEND=10020\tGT:GQ\t0/0:50\nchr1\t10021\t.\tC\tT,<*>\t50\tPASS\t.\tGT:GQ\t0/1:50\n")
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != model.FileKindGVCF {
		t.Fatalf("kind=%s", got.Kind)
	}
	if got.GVCFDialect != model.GVCFDialectDeepVariant {
		t.Fatalf("dialect=%s", got.GVCFDialect)
	}
	if got.DeepVariantVersion != "1.10.0" {
		t.Fatalf("DeepVariantVersion=%q", got.DeepVariantVersion)
	}
	if !got.HasStarAllele || got.HasNonRefAllele {
		t.Fatalf("star=%v nonref=%v", got.HasStarAllele, got.HasNonRefAllele)
	}
}

func TestInspectVariantOnlyVCF(t *testing.T) {
	path := writeGzip(t, "##fileformat=VCFv4.2\n##contig=<ID=1,length=248956422>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n1\t100\t.\tA\tG\t50\tPASS\t.\n")
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != model.FileKindVCF {
		t.Fatalf("kind=%s", got.Kind)
	}
	if got.Assembly != model.AssemblyHG38 {
		t.Fatalf("assembly=%s", got.Assembly)
	}
	if got.ContigStyle != model.ContigStyleGRCh {
		t.Fatalf("contig style=%s", got.ContigStyle)
	}
}

func TestDefaultOutputPath(t *testing.T) {
	cases := []struct {
		in   string
		mode model.ConversionMode
		dir  model.Direction
		want string
	}{
		{"sample.vcf.gz", model.ModeVariantVCF, model.DirectionForward, "sample.hg19.vcf.gz"},
		{"sample.g.vcf.gz", model.ModeGVCFCalledVariants, model.DirectionForward, "sample.hg19.vcf.gz"},
		{"sample.gvcf.vcf.gz", model.ModeGVCFPreserveExperimental, model.DirectionForward, "sample.hg19.g.vcf.gz"},
		{"sample.vcf.gz", model.ModeVariantVCF, model.DirectionReverse, "sample.hg38.vcf.gz"},
		{"sample.g.vcf.gz", model.ModeGVCFCalledVariants, model.DirectionReverse, "sample.hg38.vcf.gz"},
		{"sample.gvcf.vcf.gz", model.ModeGVCFPreserveExperimental, model.DirectionReverse, "sample.hg38.g.vcf.gz"},
	}
	for _, tc := range cases {
		if got := filepath.Base(DefaultOutputPath(tc.in, tc.mode, tc.dir)); got != tc.want {
			t.Fatalf("DefaultOutputPath(%q,%q)=%q want %q", tc.in, tc.dir, got, tc.want)
		}
	}
}

func TestInspectPartialChr22HG38(t *testing.T) {
	path := writeGzip(t, "##fileformat=VCFv4.2\n##contig=<ID=chr22,length=50818468>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nchr22\t100\t.\tA\tG\t50\tPASS\t.\n")
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Assembly != model.AssemblyHG38 {
		t.Fatalf("assembly=%s", got.Assembly)
	}
}

// hs37d5/GRCh37 headers carry the rCRS MT (16569), which collides with the
// hg38 MT length; the majority vote must still land on hg19.
func TestInspectHS37D5StyleInfersHG19(t *testing.T) {
	path := writeGzip(t, "##fileformat=VCFv4.2\n##contig=<ID=1,length=249250621,assembly=human_hs37d5.fasta>\n##contig=<ID=X,length=155270560,assembly=human_hs37d5.fasta>\n##contig=<ID=MT,length=16569,assembly=human_hs37d5.fasta>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n1\t100\t.\tA\tG\t50\tPASS\t.\n")
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Assembly != model.AssemblyHG19 {
		t.Fatalf("assembly=%s", got.Assembly)
	}
	if got.ContigStyle != model.ContigStyleGRCh {
		t.Fatalf("contig style=%s", got.ContigStyle)
	}
}

func TestInspectPartialChr22HG19(t *testing.T) {
	path := writeGzip(t, "##fileformat=VCFv4.2\n##contig=<ID=22,length=51304566>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n22\t100\t.\tA\tG\t50\tPASS\t.\n")
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Assembly != model.AssemblyHG19 {
		t.Fatalf("assembly=%s", got.Assembly)
	}
}

func TestInspectRefSeqPrimaryContig(t *testing.T) {
	path := writeGzip(t, "##fileformat=VCFv4.2\n##contig=<ID=NC_000001.11,length=248956422>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nNC_000001.11\t100\t.\tA\tG\t50\tPASS\t.\n")
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Assembly != model.AssemblyHG38 {
		t.Fatalf("assembly=%s", got.Assembly)
	}
	if got.ContigStyle != model.ContigStyleGRCh {
		t.Fatalf("contig style=%s", got.ContigStyle)
	}
}

func TestInspectDetectsAdjacentTabixIndex(t *testing.T) {
	path := writeGzip(t, "##fileformat=VCFv4.2\n##contig=<ID=chr1,length=248956422>\n##ALT=<ID=NON_REF,Description=\"any alt\">\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nchr1\t100\t.\tA\t<NON_REF>\t.\tPASS\t.\n")
	if err := os.WriteFile(path+".tbi", []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.InputIndexPath != path+".tbi" || got.InputIndexKind != "tbi" {
		t.Fatalf("index path/kind = %q/%q", got.InputIndexPath, got.InputIndexKind)
	}
}

func TestDiscoverSidecarIndexPrefersTBI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.g.vcf.gz")
	if err := os.WriteFile(path+".csi", []byte("csi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".tbi", []byte("tbi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, kind := DiscoverSidecarIndex(path)
	if got != path+".tbi" || kind != "tbi" {
		t.Fatalf("got %q %q", got, kind)
	}
}

func TestSidecarIndexFreshDetectsStaleIndex(t *testing.T) {
	d := t.TempDir()
	variant := filepath.Join(d, "sample.g.vcf.gz")
	index := variant + ".tbi"
	if err := os.WriteFile(index, []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(variant, []byte("variant"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(index, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(variant, now, now); err != nil {
		t.Fatal(err)
	}
	// The variant is newer than the index, so the sidecar is stale.
	if SidecarIndexFresh(variant, index) {
		t.Fatal("expected stale sidecar index")
	}
}

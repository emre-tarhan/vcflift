package converter

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/emre-tarhan/vcflift/internal/model"
)

func TestGVCFDefaultsToGenotypeThenLift(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sample.g.vcf.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	_, _ = gz.Write([]byte("##fileformat=VCFv4.2\n##reference=GRCh38\n##contig=<ID=chr1,length=248956422>\n##ALT=<ID=NON_REF,Description=\"any alt\">\n##INFO=<ID=END,Number=1,Type=Integer,Description=\"end\">\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\tFORMAT\tS\nchr1\t1\t.\tA\t<NON_REF>\t.\t.\tEND=10\tGT:PL\t0/0:0,90,900\n"))
	_ = gz.Close()
	_ = f.Close()

	plan, err := InspectAndPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != model.ModeGVCFGenotypeThenLift {
		t.Fatalf("mode=%s", plan.Mode)
	}
	if filepath.Base(plan.OutputPath) != "sample.hg19.vcf.gz" {
		t.Fatalf("output=%s", plan.OutputPath)
	}
}

func TestCandidateModeCompatibility(t *testing.T) {
	if !modeCompatible(model.FileKindGVCF, model.ModeGVCFCandidateVariants) {
		t.Fatal("candidate gVCF mode should be allowed")
	}
	if modeCompatible(model.FileKindVCF, model.ModeGVCFCandidateVariants) {
		t.Fatal("candidate gVCF mode should not be allowed for ordinary VCF")
	}
}

func TestDeepVariantGVCFDefaultsToCalledVariantExtraction(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sample.g.vcf.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	_, _ = gz.Write([]byte("##fileformat=VCFv4.2\n##DeepVariant_version=1.10.0\n##contig=<ID=chr1,length=248956422>\n##INFO=<ID=END,Number=1,Type=Integer,Description=\"end\">\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\tFORMAT\tS\nchr1\t10001\t.\tA\t<*>\t.\t.\tEND=10020\tGT:GQ\t0/0:50\nchr1\t10021\t.\tC\tT,<*>\t50\tPASS\t.\tGT:GQ\t0/1:50\n"))
	_ = gz.Close()
	_ = f.Close()

	plan, err := InspectAndPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != model.ModeGVCFCandidateVariants {
		t.Fatalf("mode=%s", plan.Mode)
	}
}

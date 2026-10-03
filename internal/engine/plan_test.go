package engine

import (
	"slices"
	"testing"

	"github.com/emre-tarhan/vcflift/internal/model"
)

func tc() Toolchain {
	return Toolchain{
		BCFTools: "bcftools", Java: "java", GATKJar: "gatk.jar",
		HG38FASTA: "hg38.fa", HG19FASTA: "hg19.fa", Chain38To19: "38to19.chain.gz", SourceRenameMap: "rename.tsv",
	}
}

func TestVCFPipeline(t *testing.T) {
	p, err := BuildPipeline(tc(), model.JobConfig{InputPath: "in.vcf.gz", Mode: model.ModeVariantVCF}, "reject.vcf.gz", "out.tmp.vcf.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 5 {
		t.Fatalf("steps=%d", len(p.Steps))
	}
	if p.Steps[0].Name != "validate hg38 reference alleles" {
		t.Fatalf("first=%s", p.Steps[0].Name)
	}
	if p.Steps[1].Name != "allele-aware liftover" {
		t.Fatalf("second=%s", p.Steps[1].Name)
	}
	if p.Steps[2].Name != "validate hg19 reference alleles" {
		t.Fatalf("third=%s", p.Steps[2].Name)
	}
}

func TestGVCFGenotypeThenLiftPipeline(t *testing.T) {
	p, err := BuildPipeline(tc(), model.JobConfig{InputPath: "in.g.vcf.gz", Mode: model.ModeGVCFGenotypeThenLift}, "reject.vcf.gz", "out.tmp.vcf.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 8 {
		t.Fatalf("steps=%d: %#v", len(p.Steps), p.Steps)
	}
	if p.Steps[0].Name != "validate and stage hg38 gVCF" || p.Steps[2].Name != "genotype hg38 gVCF" {
		t.Fatalf("unexpected first stages: %s / %s", p.Steps[0].Name, p.Steps[2].Name)
	}
	if !slices.Contains(p.Steps[0].Args, "-N") {
		t.Fatalf("staging validation must preserve gVCF representation: %v", p.Steps[0].Args)
	}
	args := p.Steps[2].Args
	for _, required := range []string{"GenotypeGVCFs", "-R", "hg38.fa", "-V"} {
		if !slices.Contains(args, required) {
			t.Fatalf("missing %q in %v", required, args)
		}
	}
	if len(p.TempFiles) < 3 {
		t.Fatalf("expected temporary gVCF/genotyped files, got %v", p.TempFiles)
	}
}

func TestGVCFReusesValidatedInputIndex(t *testing.T) {
	p, err := BuildPipeline(tc(), model.JobConfig{InputPath: "in.g.vcf.gz", Mode: model.ModeGVCFGenotypeThenLift, ReuseInputIndex: true}, "reject.vcf.gz", "out.tmp.vcf.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 7 {
		t.Fatalf("steps=%d: %#v", len(p.Steps), p.Steps)
	}
	if p.Steps[0].Name != "validate indexed hg38 gVCF" || p.Steps[1].Name != "genotype hg38 gVCF" {
		t.Fatalf("unexpected indexed path: %s / %s", p.Steps[0].Name, p.Steps[1].Name)
	}
	if !slices.Contains(p.Steps[0].Args, "-N") {
		t.Fatalf("indexed validation must not normalize: %v", p.Steps[0].Args)
	}
	if !slices.Contains(p.Steps[1].Args, "in.g.vcf.gz") {
		t.Fatalf("GenotypeGVCFs should read the original indexed gVCF: %v", p.Steps[1].Args)
	}
	for _, f := range p.TempFiles {
		if f == "out.tmp.vcf.gz.source.hg38.g.vcf.gz" {
			t.Fatalf("indexed input should not be restaged: %v", p.TempFiles)
		}
	}
}

func TestGVCFCandidatePipeline(t *testing.T) {
	p, err := BuildPipeline(tc(), model.JobConfig{InputPath: "in.g.vcf.gz", Mode: model.ModeGVCFCandidateVariants}, "reject.vcf.gz", "out.tmp.vcf.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 6 {
		t.Fatalf("steps=%d", len(p.Steps))
	}
	args := p.Steps[0].Args
	for _, required := range []string{"-A", "-a", "-i", `GT="alt"`} {
		if !slices.Contains(args, required) {
			t.Fatalf("missing %q in %v", required, args)
		}
	}
	if slices.Contains(args, "-v") || slices.Contains(args, "--types") {
		t.Fatalf("candidate extraction must not restrict variant TYPE; complex sequence-resolved calls are valid: %v", args)
	}
}

func TestRenameInsertedBeforeValidation(t *testing.T) {
	p, err := BuildPipeline(tc(), model.JobConfig{InputPath: "in.vcf.gz", Mode: model.ModeVariantVCF, NeedsSourceRename: true}, "reject.vcf.gz", "out.tmp.vcf.gz")
	if err != nil {
		t.Fatal(err)
	}
	if p.Steps[0].Name != "normalize chromosome names" {
		t.Fatalf("first=%s", p.Steps[0].Name)
	}
	if !slices.Contains(p.Steps[0].Args, "--rename-chrs") {
		t.Fatalf("args=%v", p.Steps[0].Args)
	}
}

func TestExperimentalGVCFBlockedUntilPreprocessorExists(t *testing.T) {
	_, err := BuildPipeline(tc(), model.JobConfig{InputPath: "in.g.vcf.gz", Mode: model.ModeGVCFPreserveExperimental}, "reject.vcf.gz", "out.tmp.vcf.gz")
	if err == nil {
		t.Fatal("expected experimental mode to be blocked")
	}
}

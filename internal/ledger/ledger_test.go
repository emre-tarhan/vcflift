package ledger

import (
	"bufio"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emre-tarhan/vcflift/internal/profile"
)

func TestClassifyClasses(t *testing.T) {
	cases := []struct {
		name    string
		rec     Record
		renamer func(string) (string, bool)
		class   Class
		reason  Reason
	}{
		{
			name:    "unchanged same pos same alleles",
			rec:     Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "G,A", Chrom: "chr1", Pos: 100, Ref: "G", Alt: "A"},
			class:   ClassUnchanged,
		},
		{
			name:    "same-locus allele swap",
			rec:     Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "A,T", Chrom: "chr1", Pos: 100, Ref: "T", Alt: "A"},
			class:   ClassSameLocusAlleleSwap,
		},
		{
			// Palindromic pair: plugin_flip=1 may ride along on the same row;
			// the class column still says the observable role exchange.
			name:    "palindromic swap with plugin flip annotation",
			rec:     Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "A,T", Chrom: "chr1", Pos: 100, Ref: "T", Alt: "A", PluginFlip: "1"},
			class:   ClassSameLocusAlleleSwap,
		},
		{
			name:    "position shift alleles identical",
			rec:     Record{SrcChrom: "chr1", SrcPos: 1000001, SrcRefAlt: "G,A", Chrom: "chr1", Pos: 1064621, Ref: "G", Alt: "A"},
			class:   ClassPositionShift,
		},
		{
			name:    "multiallelic permutation is allele index rewrite",
			rec:     Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "A,G,T", Chrom: "chr1", Pos: 100, Ref: "A", Alt: "T,G"},
			class:   ClassAlleleIndexRewrite,
		},
		{
			name:    "new reference with old REF demoted is allele index rewrite",
			rec:     Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "A,G,T", Chrom: "chr1", Pos: 100, Ref: "C", Alt: "A,G,T"},
			class:   ClassAlleleIndexRewrite,
		},
		{
			name:    "reverse-strand non-palindromic SNP same pos is no_pattern",
			rec:     Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "A,G", Chrom: "chr1", Pos: 100, Ref: "T", Alt: "C", PluginFlip: "1"},
			class:   ClassUnclassifiable,
			reason:  ReasonNoPattern,
		},
		{
			name:    "left-aligned indel is no_pattern in this version",
			rec:     Record{SrcChrom: "chr1", SrcPos: 105, SrcRefAlt: "AAAA,A", Chrom: "chr1", Pos: 100, Ref: "CAAA", Alt: "C"},
			class:   ClassUnclassifiable,
			reason:  ReasonNoPattern,
		},
		{
			name:    "swap at shifted position is no_pattern",
			rec:     Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "A,T", Chrom: "chr1", Pos: 250, Ref: "T", Alt: "A"},
			class:   ClassUnclassifiable,
			reason:  ReasonNoPattern,
		},
		{
			name:    "missing SRC annotations",
			rec:     Record{Chrom: "chr1", Pos: 100, Ref: "G", Alt: "A"},
			class:   ClassUnclassifiable,
			reason:  ReasonMissingSrc,
		},
		{
			name:    "contig mismatch without renamer",
			rec:     Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "G,A", Chrom: "chr2", Pos: 100, Ref: "G", Alt: "A"},
			class:   ClassUnclassifiable,
			reason:  ReasonContigMismatch,
		},
		{
			name: "profile rename normalized before compare",
			rec:  Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "G,A", Chrom: "1", Pos: 100, Ref: "G", Alt: "A"},
			renamer: func(name string) (string, bool) {
				if name == "1" {
					return "chr1", true
				}
				return "", false
			},
			class: ClassUnchanged,
		},
		{
			name: "renamer rejects unmapped contig",
			rec:  Record{SrcChrom: "chr1", SrcPos: 100, SrcRefAlt: "G,A", Chrom: "GL000220.1", Pos: 100, Ref: "G", Alt: "A"},
			renamer: func(name string) (string, bool) {
				return "", false
			},
			class:  ClassUnclassifiable,
			reason: ReasonContigMismatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.rec, tc.renamer)
			if got.Class != tc.class || got.Reason != tc.reason {
				t.Fatalf("Classify = %s/%s, want %s/%s", got.Class, got.Reason, tc.class, tc.reason)
			}
		})
	}
}

func TestGRCh37RenamerFromProfile(t *testing.T) {
	if s, ok := profile.GRCh37ToUCSC("1"); !ok || s != "chr1" {
		t.Fatalf("GRCh37ToUCSC(1) = %q,%v", s, ok)
	}
	if _, ok := profile.GRCh37ToUCSC("MT"); ok {
		t.Fatal("MT must not be renameable (profile rejects it earlier)")
	}
}

const testVCF = `##fileformat=VCFv4.2
##INFO=<ID=SRC_CHROM,Number=1,Type=String,Description="src contig">
##INFO=<ID=SRC_POS,Number=1,Type=Integer,Description="src pos">
##INFO=<ID=SRC_REF_ALT,Number=.,Type=String,Description="src alleles">
##INFO=<ID=FLIP,Number=0,Type=Flag,Description="flip">
##INFO=<ID=SWAP,Number=1,Type=Integer,Description="swap">
#CHROM	POS	ID	REF	ALT	QUAL	FILTER	INFO
chr1	1064621	.	G	A	50	PASS	SRC_CHROM=chr1;SRC_POS=1000001;SRC_REF_ALT=G,A
chr1	200	.	T	A	50	PASS	SRC_CHROM=chr1;SRC_POS=200;SRC_REF_ALT=A,T;FLIP;SWAP=0
chr1	300	.	T	C	50	PASS	SRC_CHROM=chr1;SRC_POS=300;SRC_REF_ALT=A,G;FLIP
chr1	400	.	G	A	50	PASS	.
chr1	500	.	C	CA	50	PASS	SRC_CHROM=chr1;SRC_POS=505;SRC_REF_ALT=CAAA,CA
`

func TestWriteSidecarRowsAndCounts(t *testing.T) {
	dir := t.TempDir()
	vcfPath := filepath.Join(dir, "out.vcf")
	if err := os.WriteFile(vcfPath, []byte(testVCF), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "out.ledger.tsv.gz")
	counts, err := WriteSidecar(vcfPath, sidecar, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if counts.ClassifiedRecords != 5 {
		t.Fatalf("classified = %d, want 5", counts.ClassifiedRecords)
	}
	if counts.PositionShift != 1 || counts.SameLocusAlleleSwap != 1 || counts.Unclassifiable != 3 || counts.Unchanged != 0 {
		t.Fatalf("counts = %+v", counts)
	}
	if counts.LeftAlignRepresentationChange != 0 {
		t.Fatalf("reserved class must stay zero, got %d", counts.LeftAlignRepresentationChange)
	}
	if counts.PluginFlipRecords != 2 || counts.PluginSwapRecords != 1 {
		t.Fatalf("plugin counts = flip %d swap %d, want 2/1", counts.PluginFlipRecords, counts.PluginSwapRecords)
	}
	if sum := counts.Unchanged + counts.SameLocusAlleleSwap + counts.PositionShift + counts.LeftAlignRepresentationChange + counts.AlleleIndexRewrite + counts.Unclassifiable; sum != counts.ClassifiedRecords {
		t.Fatalf("sum(classes)=%d != classified=%d", sum, counts.ClassifiedRecords)
	}

	f, err := os.Open(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var lines []string
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 10 { // 4 comments + header + 5 rows
		t.Fatalf("sidecar line count = %d, want 10", len(lines))
	}
	if !strings.HasPrefix(lines[4], "#src_chrom\tsrc_pos") {
		t.Fatalf("header row = %q", lines[4])
	}
	rows := lines[5:]
	want := [][]string{
		{"chr1", "1000001", "G", "A", "chr1", "1064621", "G", "A", "position_shift", ".", ".", "."},
		{"chr1", "200", "A", "T", "chr1", "200", "T", "A", "same_locus_allele_swap", "1", "0", "."},
		{"chr1", "300", "A", "G", "chr1", "300", "T", "C", "unclassifiable", "1", ".", "no_pattern"},
		{".", ".", ".", ".", "chr1", "400", "G", "A", "unclassifiable", ".", ".", "missing_src"},
		{"chr1", "505", "CAAA", "CA", "chr1", "500", "C", "CA", "unclassifiable", ".", ".", "no_pattern"},
	}
	for i, w := range want {
		got := strings.Split(rows[i], "\t")
		if len(got) != len(w) {
			t.Fatalf("row %d field count %d: %q", i, len(got), rows[i])
		}
		for j := range w {
			if got[j] != w[j] {
				t.Fatalf("row %d field %d = %q, want %q (row %q)", i, j, got[j], w[j], rows[i])
			}
		}
	}
}

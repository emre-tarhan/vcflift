package profile

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Profile{
		"":               UCSCHg19,
		"ucsc-hg19":      UCSCHg19,
		"grch37-primary": GRCh37Primary,
		"hs37d5":         HS37D5,
	} {
		got, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("Parse(%q)=%q want %q", in, got, want)
		}
	}
	if _, err := Parse("b37"); err == nil {
		t.Fatal("Parse(b37) should fail")
	}
}

const splitInput = `##fileformat=VCFv4.2
##reference=hg19
##contig=<ID=chr1,length=249250621>
##contig=<ID=chrX,length=155270560>
##contig=<ID=chrM,length=16571>
##contig=<ID=chrUn_gl000220v1,length=161802>
##FORMAT=<ID=GT,Number=1,Type=String,Description="Genotype">
#CHROM	POS	ID	REF	ALT	QUAL	FILTER	INFO	FORMAT	S1
chr1	10001	.	A	G	50	PASS	.	GT	0/1
chr1	10002	.	T	C	50	PASS	.	GT	1/1
chrX	20001	.	C	T	50	PASS	.	GT	0/1
chrM	101	.	T	C	50	PASS	.	GT	0/1
chrUn_gl000220v1	500	.	G	A	50	PASS	.	GT	0/1
`

func TestSplitGRCh37Primary(t *testing.T) {
	var primary, reject bytes.Buffer
	stats, err := Split(strings.NewReader(splitInput), GRCh37Primary, &primary, &reject)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Kept != 3 || stats.StaleChrM != 1 || stats.NonPrimaryContig != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	if stats.PreProfileRecords() != 5 {
		t.Fatalf("pre-profile=%d", stats.PreProfileRecords())
	}
	rej := stats.ProfileRejects()
	if rej[FilterStaleHg19ChrM] != 1 || rej[FilterNonPrimaryContig] != 1 {
		t.Fatalf("profile rejects=%v", rej)
	}

	p := primary.String()
	if !strings.Contains(p, "##vcflift_target_profile=grch37-primary") {
		t.Fatal("primary header missing profile line")
	}
	if !strings.Contains(p, "##contig=<ID=1,length=249250621>") || !strings.Contains(p, "##contig=<ID=MT,length=16569>") {
		t.Fatal("primary header missing GRCh37 dictionary bounds")
	}
	if strings.Contains(p, "ID=chr") {
		t.Fatal("primary header still has UCSC contig lines")
	}
	if !strings.Contains(p, "##FORMAT=<ID=GT") || !strings.Contains(p, "##reference=hg19") {
		t.Fatal("primary header lost non-contig meta lines")
	}
	if !strings.Contains(p, "\n1\t10001\t") || !strings.Contains(p, "\nX\t20001\t") {
		t.Fatal("primary records not renamed")
	}
	if !strings.Contains(p, "\n1\t10002\t.\tT\tC\t50\tPASS\t") {
		t.Fatal("primary record FILTER/INFO not preserved")
	}

	rj := reject.String()
	if !strings.Contains(rj, "##FILTER=<ID="+FilterStaleHg19ChrM) || !strings.Contains(rj, "##FILTER=<ID="+FilterNonPrimaryContig) {
		t.Fatal("reject bucket header missing filter lines")
	}
	if !strings.Contains(rj, "\nchrM\t101\t.\tT\tC\t50\t"+FilterStaleHg19ChrM+"\t") {
		t.Fatalf("chrM record not marked: %s", rj)
	}
	if !strings.Contains(rj, "\nchrUn_gl000220v1\t500\t.\tG\tA\t50\t"+FilterNonPrimaryContig+"\t") {
		t.Fatalf("non-primary record not marked: %s", rj)
	}
}

func TestSplitHS37D5Note(t *testing.T) {
	var primary, reject bytes.Buffer
	if _, err := Split(strings.NewReader(splitInput), HS37D5, &primary, &reject); err != nil {
		t.Fatal(err)
	}
	p := primary.String()
	if !strings.Contains(p, "##vcflift_target_profile=hs37d5") {
		t.Fatal("hs37d5 profile line missing")
	}
	if !strings.Contains(p, "decoy contigs are not produced") {
		t.Fatal("hs37d5 decoy note missing")
	}
	if !strings.Contains(p, "\n1\t10001\t") {
		t.Fatal("hs37d5 should rename like grch37-primary")
	}
}

func TestSplitEmptyRejectsHaveNoHeader(t *testing.T) {
	input := strings.ReplaceAll(splitInput, "chrM\t101\t.\tT\tC\t50\tPASS\t.\tGT\t0/1\n", "")
	input = strings.ReplaceAll(input, "chrUn_gl000220v1\t500\t.\tG\tA\t50\tPASS\t.\tGT\t0/1\n", "")
	var primary, reject bytes.Buffer
	stats, err := Split(strings.NewReader(input), GRCh37Primary, &primary, &reject)
	if err != nil {
		t.Fatal(err)
	}
	if stats.StaleChrM != 0 || stats.NonPrimaryContig != 0 || stats.Kept != 3 {
		t.Fatalf("stats=%+v", stats)
	}
	if reject.Len() != 0 {
		t.Fatal("empty reject bucket must stay empty")
	}
	if stats.ProfileRejects() != nil {
		t.Fatal("no rejects expected")
	}
}

func TestGRCh37HeaderContigLines(t *testing.T) {
	lines := GRCh37HeaderContigLines()
	if len(lines) != 25 {
		t.Fatalf("contig lines=%d want 25", len(lines))
	}
	if lines[0] != "##contig=<ID=1,length=249250621>" || lines[24] != "##contig=<ID=MT,length=16569>" {
		t.Fatalf("dictionary bounds wrong: %s .. %s", lines[0], lines[24])
	}
}

func writeMiniFASTA(t *testing.T, dir string) (fastaPath string) {
	t.Helper()
	// contig "1": 100 bases, width 60; contig "2": 4 bases.
	seq1 := "ACGTACGTAC" + strings.Repeat("N", 90)
	seq2 := "TTTT"
	var b strings.Builder
	b.WriteString(">1\n")
	for i := 0; i < len(seq1); i += 60 {
		end := i + 60
		if end > len(seq1) {
			end = len(seq1)
		}
		b.WriteString(seq1[i:end] + "\n")
	}
	b.WriteString(">2\n" + seq2 + "\n")
	fastaPath = dir + "/grch37_test.fa"
	if err := os.WriteFile(fastaPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	// offsets: ">1\n"=3, seq1 with newline = 101+... compute: 10 lines of 61 except last
	// line lengths: 61*1 (60+nl) for first, last line 40+1
	fai := "1\t100\t3\t60\t61\n"
	off2 := 3 + 61 + 41 + 3 // ">1\n" + first line + last line + ">2\n"
	fai += "2\t4\t" + strconv.FormatInt(int64(off2), 10) + "\t4\t5\n"
	if err := os.WriteFile(fastaPath+".fai", []byte(fai), 0o644); err != nil {
		t.Fatal(err)
	}
	return fastaPath
}

func TestCheckREF(t *testing.T) {
	dir := t.TempDir()
	fasta := writeMiniFASTA(t, dir)
	vcfOK := "##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n1\t1\t.\tA\tG\t.\tPASS\t.\n1\t3\t.\tGTAC\tG\t.\tPASS\t.\n1\t11\t.\tN\tA\t.\tPASS\t.\n2\t2\t.\tT\tC\t.\tPASS\t.\n"
	p := filepath.Join(dir, "ok.vcf")
	if err := os.WriteFile(p, []byte(vcfOK), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckREF(p, fasta); err != nil {
		t.Fatalf("matching REF should pass: %v", err)
	}

	vcfBad := "##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n1\t2\t.\tA\tG\t.\tPASS\t.\n"
	pb := filepath.Join(dir, "bad.vcf")
	if err := os.WriteFile(pb, []byte(vcfBad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckREF(pb, fasta); err == nil {
		t.Fatal("mismatched REF should fail")
	}

	vcfFar := "##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n1\t150\t.\tA\tG\t.\tPASS\t.\n"
	pf := filepath.Join(dir, "far.vcf")
	if err := os.WriteFile(pf, []byte(vcfFar), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckREF(pf, fasta); err == nil {
		t.Fatal("record past contig end should fail")
	}

	vcfMissing := "##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nMT\t1\t.\tA\tG\t.\tPASS\t.\n"
	pm := filepath.Join(dir, "missing.vcf")
	if err := os.WriteFile(pm, []byte(vcfMissing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckREF(pm, fasta); err == nil {
		t.Fatal("missing contig should fail")
	}
}

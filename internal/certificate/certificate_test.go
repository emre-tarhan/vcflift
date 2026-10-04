package certificate

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/emre-tarhan/vcflift/internal/profile"
)

// writeFai writes a minimal faidx file (name, length, offset, linebases,
// linewidth) for the given contigs.
func writeFai(t *testing.T, dir string, contigs [][2]any) string {
	t.Helper()
	var b strings.Builder
	offset := 0
	for _, c := range contigs {
		name := c[0].(string)
		length := c[1].(int64)
		b.WriteString(name)
		b.WriteString("\t")
		b.WriteString(strconv.FormatInt(length, 10))
		b.WriteString("\t")
		b.WriteString(strconv.Itoa(offset))
		b.WriteString("\t60\t61\n")
		offset += int(length)
	}
	p := filepath.Join(dir, "ref.fa.fai")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fa := filepath.Join(dir, "ref.fa")
	if err := os.WriteFile(fa, []byte(">stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fa
}

var miniDict = []profile.Contig{
	{Name: "1", Length: 1000},
	{Name: "2", Length: 900},
	{Name: "MT", Length: 16569},
}

func TestCompareCompatible(t *testing.T) {
	dir := t.TempDir()
	fa := writeFai(t, dir, [][2]any{{"1", int64(1000)}, {"2", int64(900)}, {"MT", int64(16569)}})
	rep, err := Compare(fa, "", miniDict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictCompatible || rep.FirstReason != "" {
		t.Fatalf("verdict = %s %q", rep.Verdict, rep.FirstReason)
	}
	if rep.MatchedContigs != 3 || rep.ExpectedContigs != 3 {
		t.Fatalf("matched = %d/%d", rep.MatchedContigs, rep.ExpectedContigs)
	}
	if rep.PARMeasured {
		t.Fatal("PARMeasured must be false")
	}
	want := "Compatible with the grch37-primary dictionary: 3 primary contigs, all lengths match, MT is the rCRS (16569). PAR masking is not measured and is not part of this verdict."
	if got := rep.Statement("grch37-primary"); got != want {
		t.Fatalf("statement = %q", got)
	}
}

func TestCompareHg19MTIsIncompatible(t *testing.T) {
	dir := t.TempDir()
	// hg19-style FASTA: chr-prefixed names, 1/2 lengths identical, chrM 16571.
	fa := writeFai(t, dir, [][2]any{{"chr1", int64(1000)}, {"chr2", int64(900)}, {"chrM", int64(16571)}})
	rep, err := Compare(fa, "", miniDict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictIncompatible {
		t.Fatalf("verdict = %s", rep.Verdict)
	}
	want := "MT length 16571 is the old NC_001807 sequence (hg19 chrM); this dictionary carries the rCRS MT (16569)"
	if rep.FirstReason != want {
		t.Fatalf("first reason = %q", rep.FirstReason)
	}
	if len(rep.LengthMismatches) != 1 || rep.LengthMismatches[0].Contig != "MT" {
		t.Fatalf("mismatches = %+v", rep.LengthMismatches)
	}
	statement := rep.Statement("grch37-primary")
	if !strings.Contains(statement, "The FASTA was not modified.") || !strings.Contains(statement, "PAR masking is not measured") {
		t.Fatalf("statement = %q", statement)
	}
}

func TestCompareMissingContigFirst(t *testing.T) {
	dir := t.TempDir()
	fa := writeFai(t, dir, [][2]any{{"chr1", int64(1000)}, {"chrM", int64(16571)}}) // 2 missing... 1 missing + MT wrong
	rep, err := Compare(fa, "", miniDict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictIncompatible {
		t.Fatalf("verdict = %s", rep.Verdict)
	}
	if !strings.HasPrefix(rep.FirstReason, "contig 2 is missing from the FASTA") {
		t.Fatalf("first reason = %q (missing must outrank MT length)", rep.FirstReason)
	}
}

func TestCompareOtherLengthMismatch(t *testing.T) {
	dir := t.TempDir()
	fa := writeFai(t, dir, [][2]any{{"1", int64(1001)}, {"2", int64(900)}, {"MT", int64(16569)}})
	rep, err := Compare(fa, "", miniDict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	want := "contig 1 length 1001 does not match the expected 1000"
	if rep.Verdict != VerdictIncompatible || rep.FirstReason != want {
		t.Fatalf("verdict/reason = %s / %q", rep.Verdict, rep.FirstReason)
	}
}

func TestCompareDecoyExtra(t *testing.T) {
	dir := t.TempDir()
	fa := writeFai(t, dir, [][2]any{{"1", int64(1000)}, {"2", int64(900)}, {"MT", int64(16569)}, {"GL000220.1_decoy", int64(500)}})
	rep, err := Compare(fa, "", miniDict, "hs37d5")
	if err != nil {
		t.Fatal(err)
	}
	want := "1 decoy contigs present (e.g. GL000220.1_decoy); the hs37d5 profile dictionary is primary-only and does not include decoys"
	if rep.Verdict != VerdictIncompatible || rep.FirstReason != want {
		t.Fatalf("verdict/reason = %s / %q", rep.Verdict, rep.FirstReason)
	}
	if rep.ExtraContigs.Decoy != 1 || rep.ExtraContigs.Other != 0 || rep.ExtraContigs.EBV {
		t.Fatalf("extras = %+v", rep.ExtraContigs)
	}
}

func TestCompareEBVExtra(t *testing.T) {
	dir := t.TempDir()
	fa := writeFai(t, dir, [][2]any{{"1", int64(1000)}, {"2", int64(900)}, {"MT", int64(16569)}, {"NC_007605.1", int64(171523)}})
	rep, err := Compare(fa, "", miniDict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	want := "EBV contig present (NC_007605.1); the grch37-primary dictionary does not include EBV"
	if rep.Verdict != VerdictIncompatible || rep.FirstReason != want {
		t.Fatalf("verdict/reason = %s / %q", rep.Verdict, rep.FirstReason)
	}
}

func TestCompareOtherExtra(t *testing.T) {
	dir := t.TempDir()
	fa := writeFai(t, dir, [][2]any{{"1", int64(1000)}, {"2", int64(900)}, {"MT", int64(16569)}, {"GL000221.1", int64(155261)}})
	rep, err := Compare(fa, "", miniDict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	want := "1 unexpected contigs present (e.g. GL000221.1)"
	if rep.Verdict != VerdictIncompatible || rep.FirstReason != want {
		t.Fatalf("verdict/reason = %s / %q", rep.Verdict, rep.FirstReason)
	}
}

func TestCompareAliasNormalization(t *testing.T) {
	dir := t.TempDir()
	fa := writeFai(t, dir, [][2]any{
		{"NC_000001.10", int64(1000)}, // accession names, mapped via chromAlias
		{"NC_000002.11", int64(900)},
		{"NC_012920.1", int64(16569)}, // rCRS MT accession
	})
	aliasPath := filepath.Join(dir, "chromAlias.txt")
	alias := "chr1\tNC_000001.10\t1\nchr2\tNC_000002.11\t2\nchrM\tNC_012920.1\tMT\tM\n"
	if err := os.WriteFile(aliasPath, []byte(alias), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Compare(fa, aliasPath, miniDict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictCompatible {
		t.Fatalf("verdict = %s, reason %q", rep.Verdict, rep.FirstReason)
	}
}

func TestCompareDuplicateContigIsExtra(t *testing.T) {
	dir := t.TempDir()
	fa := writeFai(t, dir, [][2]any{{"1", int64(1000)}, {"chr1", int64(1000)}, {"2", int64(900)}, {"MT", int64(16569)}})
	rep, err := Compare(fa, "", miniDict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictIncompatible {
		t.Fatalf("strict set rule: duplicate contig must be incompatible, got %s", rep.Verdict)
	}
	if !strings.Contains(rep.FirstReason, "unexpected contigs present") {
		t.Fatalf("first reason = %q", rep.FirstReason)
	}
}

func TestCompareMissingFaiIsError(t *testing.T) {
	dir := t.TempDir()
	if _, err := Compare(filepath.Join(dir, "none.fa"), "", miniDict, "grch37-primary"); err == nil {
		t.Fatal("missing .fai must be an error, not a verdict")
	}
}

func TestFullGRCh37DictionaryStatement(t *testing.T) {
	dir := t.TempDir()
	dict := profile.GRCh37PrimaryContigs()
	if len(dict) != 25 {
		t.Fatalf("GRCh37 primary dictionary has %d contigs, want 25", len(dict))
	}
	var contigs [][2]any
	for _, c := range dict {
		contigs = append(contigs, [2]any{c.Name, c.Length})
	}
	fa := writeFai(t, dir, contigs)
	rep, err := Compare(fa, "", dict, "grch37-primary")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictCompatible || rep.MatchedContigs != 25 {
		t.Fatalf("verdict = %s matched = %d reason %q", rep.Verdict, rep.MatchedContigs, rep.FirstReason)
	}
}

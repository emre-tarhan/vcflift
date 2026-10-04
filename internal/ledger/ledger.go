// Package ledger classifies what the liftover did to each lifted record and
// writes a per-record sidecar. Classes are decided from record letters only
// (SRC_* INFO annotations vs the output record); the plugin's FLIP/SWAP tags
// are copied verbatim into the sidecar and never classified. There is no
// strand category. docs/LEDGER.md is the binding design; nothing here may
// contradict it.
package ledger

import (
	"bufio"
	"compress/gzip"
	"os"
	"strconv"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/profile"
)

type Class string

const (
	ClassUnchanged           Class = "unchanged"
	ClassSameLocusAlleleSwap Class = "same_locus_allele_swap"
	ClassPositionShift       Class = "position_shift"
	// ClassLeftAlignRepresentationChange is reserved: it needs the
	// pre-left-align target position, which no current field carries. It is
	// never assigned in this version and its counter stays zero in every
	// report (docs/LEDGER.md Section 4.1).
	ClassLeftAlignRepresentationChange Class = "left_align_representation_change"
	ClassAlleleIndexRewrite            Class = "allele_index_rewrite"
	ClassUnclassifiable                Class = "unclassifiable"
)

type Reason string

const (
	ReasonMissingSrc     Reason = "missing_src"
	ReasonContigMismatch Reason = "contig_mismatch"
	ReasonNoPattern      Reason = "no_pattern"
)

// Record is one lifted VCF record, reduced to the fields the classifier and
// the sidecar need. PluginFlip/PluginSwap carry the plugin's FLIP/SWAP INFO
// values verbatim ("1"/"0" and an integer, "" when the tag is absent); they
// are annotations, never inputs to the class.
type Record struct {
	SrcChrom   string
	SrcPos     int64
	SrcRefAlt  string // SRC_REF_ALT: REF first, then ALTs
	Chrom      string
	Pos        int64
	Ref, Alt   string
	PluginFlip string
	PluginSwap string
}

type Outcome struct {
	Class  Class
	Reason Reason // set only for ClassUnclassifiable
}

// Classify runs the letter tree of docs/LEDGER.md Section 4. renamer maps an
// output contig name back to source-side naming for profiles that rename
// contigs (nil for ucsc-hg19); it is the only place naming is normalized.
func Classify(r Record, renamer func(string) (string, bool)) Outcome {
	if r.SrcChrom == "" || r.SrcPos <= 0 || r.SrcRefAlt == "" || r.Chrom == "" || r.Pos <= 0 {
		return Outcome{Class: ClassUnclassifiable, Reason: ReasonMissingSrc}
	}
	outContig := r.Chrom
	if renamer != nil {
		mapped, ok := renamer(outContig)
		if !ok {
			return Outcome{Class: ClassUnclassifiable, Reason: ReasonContigMismatch}
		}
		outContig = mapped
	}
	if outContig != r.SrcChrom {
		return Outcome{Class: ClassUnclassifiable, Reason: ReasonContigMismatch}
	}
	src := strings.Split(r.SrcRefAlt, ",")
	out := append([]string{r.Ref}, strings.Split(r.Alt, ",")...)
	if r.Pos == r.SrcPos {
		if identical(src, out) {
			return Outcome{Class: ClassUnchanged}
		}
		if len(src) == 2 && len(out) == 2 && src[0] == out[1] && src[1] == out[0] {
			return Outcome{Class: ClassSameLocusAlleleSwap}
		}
		if roleRewrite(src, out) {
			return Outcome{Class: ClassAlleleIndexRewrite}
		}
		return Outcome{Class: ClassUnclassifiable, Reason: ReasonNoPattern}
	}
	if identical(src, out) {
		return Outcome{Class: ClassPositionShift}
	}
	// left_align_representation_change is reserved and not assigned in this
	// version (docs/LEDGER.md Section 4.1): a moved position with changed
	// letters is not classifiable from the observable record.
	return Outcome{Class: ClassUnclassifiable, Reason: ReasonNoPattern}
}

func identical(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// roleRewrite reports whether the allele collection was rearranged in place
// at the same locus: a permutation of the same alleles (multiallelic index
// rewrite), or a new REF introduced while the old REF was demoted to ALT.
// Biallelic role exchange is classified earlier as same_locus_allele_swap;
// it also matches the permutation branch here, so tree order matters.
func roleRewrite(src, out []string) bool {
	if equalMultiset(src, out) {
		return true
	}
	if contains(src, out[0]) {
		return false
	}
	if !contains(out[1:], src[0]) {
		return false
	}
	return equalMultiset(removeOne(src, src[0]), removeOne(out[1:], src[0]))
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func removeOne(set []string, v string) []string {
	out := make([]string, 0, len(set))
	removed := false
	for _, s := range set {
		if !removed && s == v {
			removed = true
			continue
		}
		out = append(out, s)
	}
	return out
}

func equalMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
		if counts[s] < 0 {
			return false
		}
	}
	return true
}

// Counts is the QC summary carried by the report and the GUI. The six class
// counters are frozen identifiers; left_align_representation_change is always
// zero in this version and exists so the schema does not change when the
// class one day becomes assignable.
type Counts struct {
	ClassifiedRecords             int64 `json:"classified_records"`
	Unchanged                     int64 `json:"unchanged"`
	SameLocusAlleleSwap           int64 `json:"same_locus_allele_swap"`
	PositionShift                 int64 `json:"position_shift"`
	LeftAlignRepresentationChange int64 `json:"left_align_representation_change"`
	AlleleIndexRewrite            int64 `json:"allele_index_rewrite"`
	Unclassifiable                int64 `json:"unclassifiable"`
	PluginFlipRecords             int64 `json:"plugin_flip_records"`
	PluginSwapRecords             int64 `json:"plugin_swap_records"`
}

// SidecarHeader is the TSV column row of the sidecar, after the '#'-comment
// provenance lines.
const SidecarHeader = "src_chrom\tsrc_pos\tsrc_ref\tsrc_alt\tchrom\tpos\tref\talt\tclass\tplugin_flip\tplugin_swap\treason"

// WriteSidecar reads a finalized output VCF (plain or bgzip), classifies every
// data record, writes <outPath> as a gz TSV sidecar, and returns the counts.
// A record whose SRC_* annotations are missing or malformed is written with
// '.' source fields and classified unclassifiable/missing_src; nothing is
// dropped. The output VCF is only read.
func WriteSidecar(vcfPath, outPath string, renamer func(string) (string, bool), toolVersion string) (Counts, error) {
	var counts Counts
	r, closeFn, err := profile.OpenText(vcfPath)
	if err != nil {
		return counts, err
	}
	defer closeFn()

	tmp := outPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return counts, err
	}
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	w.WriteString("# vcflift conversion ledger v1 (tool " + toolVersion + ")\n")
	w.WriteString("# classes: unchanged same_locus_allele_swap position_shift left_align_representation_change allele_index_rewrite unclassifiable\n")
	w.WriteString("# left_align_representation_change is reserved and not assigned in this version\n")
	w.WriteString("# plugin_flip/plugin_swap are the plugin's FLIP/SWAP annotations copied verbatim; they are not ledger classes\n")
	w.WriteString("#" + SidecarHeader + "\n")

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		rec, ok := parseRecord(line)
		if !ok {
			counts.ClassifiedRecords++
			counts.Unclassifiable++
			if _, err := w.WriteString("\t.\t.\t.\t.\t.\t.\t.\t" + string(ClassUnclassifiable) + "\t.\t.\t" + string(ReasonMissingSrc) + "\n"); err != nil {
				return counts, err
			}
			continue
		}
		outcome := Classify(rec, renamer)
		counts.ClassifiedRecords++
		switch outcome.Class {
		case ClassUnchanged:
			counts.Unchanged++
		case ClassSameLocusAlleleSwap:
			counts.SameLocusAlleleSwap++
		case ClassPositionShift:
			counts.PositionShift++
		case ClassAlleleIndexRewrite:
			counts.AlleleIndexRewrite++
		case ClassUnclassifiable:
			counts.Unclassifiable++
		case ClassLeftAlignRepresentationChange:
			// Unreachable by construction: Classify never assigns it.
			counts.Unclassifiable++
		}
		if rec.PluginFlip != "" {
			counts.PluginFlipRecords++
		}
		if rec.PluginSwap != "" {
			counts.PluginSwapRecords++
		}
		reason := "."
		if outcome.Reason != "" {
			reason = string(outcome.Reason)
		}
		srcRef, srcAlt := splitRefAlt(rec.SrcRefAlt)
		srcPos := "."
		if rec.SrcPos > 0 {
			srcPos = strconv.FormatInt(rec.SrcPos, 10)
		}
		flip := orDot(rec.PluginFlip)
		swap := orDot(rec.PluginSwap)
		if _, err := w.WriteString(strings.Join([]string{
			orDot(rec.SrcChrom), srcPos, srcRef, srcAlt,
			rec.Chrom, strconv.FormatInt(rec.Pos, 10), rec.Ref, rec.Alt,
			string(outcome.Class), flip, swap, reason,
		}, "\t") + "\n"); err != nil {
			return counts, err
		}
	}
	if err := sc.Err(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return counts, err
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return counts, err
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return counts, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return counts, err
	}
	return counts, os.Rename(tmp, outPath)
}

func orDot(v string) string {
	if v == "" {
		return "."
	}
	return v
}

func splitRefAlt(refAlt string) (string, string) {
	parts := strings.Split(refAlt, ",")
	if len(parts) <= 1 {
		return orDot(refAlt), "."
	}
	return parts[0], strings.Join(parts[1:], ",")
}

// parseRecord extracts CHROM/POS/REF/ALT and the SRC_*/FLIP/SWAP INFO
// annotations from one VCF data line. ok is false for structurally malformed
// lines (fewer than 5 tab fields).
func parseRecord(line string) (Record, bool) {
	var rec Record
	f := strings.Split(line, "\t")
	if len(f) < 5 {
		return rec, false
	}
	rec.Chrom = f[0]
	pos, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || pos <= 0 {
		return rec, false
	}
	rec.Pos = pos
	rec.Ref = f[3]
	rec.Alt = f[4]
	if len(f) >= 8 {
		for _, kv := range strings.Split(f[7], ";") {
			if kv == "" {
				continue
			}
			key, val, hasVal := strings.Cut(kv, "=")
			switch key {
			case "SRC_CHROM":
				rec.SrcChrom = val
			case "SRC_POS":
				if p, perr := strconv.ParseInt(val, 10, 64); perr == nil {
					rec.SrcPos = p
				}
			case "SRC_REF_ALT":
				rec.SrcRefAlt = val
			case "FLIP":
				rec.PluginFlip = "1"
			case "SWAP":
				if hasVal {
					rec.PluginSwap = val
				} else {
					rec.PluginSwap = "."
				}
			}
		}
	}
	return rec, true
}

// Package profile implements target naming profiles for the hg19 output.
// The liftover itself always targets UCSC hg19; a profile renames and filters
// the lifted output afterwards without touching the validated pipeline.
package profile

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Profile string

const (
	// UCSCHg19 is the default: chr-prefixed UCSC hg19 naming, unchanged output.
	UCSCHg19 Profile = "ucsc-hg19"
	// GRCh37Primary renames primary contigs to GRCh37 naming (1-22, X, Y),
	// drops chrM records into a stale_hg19_chrM bucket (hg19 chrM is the old
	// NC_001807 sequence, not the GRCh37 rCRS MT), and drops every
	// non-primary contig into a non_primary_contig bucket.
	GRCh37Primary Profile = "grch37-primary"
	// HS37D5 uses the same GRCh37 primary naming; hs37d5 decoy contigs are
	// not produced.
	HS37D5 Profile = "hs37d5"
	// GRCh38Primary renames the lifted UCSC hg38 output of the REVERSE
	// direction (hg19 -> hg38) to GRCh38 primary naming (1-22, X, Y, MT) and
	// drops every non-primary contig into a non_primary_contig bucket.
	// Unlike hg19 chrM, UCSC hg38 chrM is the rCRS — sequence-identical to
	// the GRCh38 MT — so chrM records carry over as MT instead of being
	// rejected.
	GRCh38Primary Profile = "grch38-primary"
)

// Reject FILTER identifiers written into the profile reject buckets.
const (
	FilterStaleHg19ChrM    = "stale_hg19_chrM"
	FilterNonPrimaryContig = "non_primary_contig"
)

func Parse(s string) (Profile, error) {
	switch s {
	case "", string(UCSCHg19):
		return UCSCHg19, nil
	case string(GRCh37Primary):
		return GRCh37Primary, nil
	case string(HS37D5):
		return HS37D5, nil
	case string(GRCh38Primary):
		return GRCh38Primary, nil
	default:
		return "", fmt.Errorf("unknown target profile %q (expected ucsc-hg19, grch37-primary, hs37d5, or grch38-primary)", s)
	}
}

func (p Profile) IsDefault() bool { return p == UCSCHg19 }

// RenamesToGRCh37 reports whether the profile rewrites the lifted UCSC hg19
// output into GRCh37 primary naming.
func (p Profile) RenamesToGRCh37() bool { return p == GRCh37Primary || p == HS37D5 }

// RenamesContigs reports whether the profile rewrites the lifted UCSC output
// into primary contig naming at all (GRCh37 naming on the forward hg38 -> hg19
// direction, GRCh38 naming on the reverse hg19 -> hg38 direction).
func (p Profile) RenamesContigs() bool { return p.RenamesToGRCh37() || p == GRCh38Primary }

// grch37PrimaryContigs is the GRCh37 primary contig dictionary in sort order.
// Lengths are sequence-identical to UCSC hg19 for 1-22/X/Y; MT is the rCRS
// (16569 bp) which differs from hg19 chrM (16571 bp, NC_001807).
var grch37PrimaryContigs = []struct {
	Name   string
	Length int64
}{
	{"1", 249250621}, {"2", 243199373}, {"3", 198022430}, {"4", 191154276},
	{"5", 180915260}, {"6", 171115067}, {"7", 159138663}, {"8", 146364022},
	{"9", 141213431}, {"10", 135534747}, {"11", 135006516}, {"12", 133851895},
	{"13", 115169878}, {"14", 107349540}, {"15", 102531392}, {"16", 90354753},
	{"17", 81195210}, {"18", 78077248}, {"19", 59128983}, {"20", 63025520},
	{"21", 48129895}, {"22", 51304566}, {"X", 155270560}, {"Y", 59373566},
	{"MT", 16569},
}

var ucscToGRCh37 = func() map[string]string {
	m := make(map[string]string, len(grch37PrimaryContigs)-1)
	for _, c := range grch37PrimaryContigs {
		if c.Name != "MT" {
			m["chr"+c.Name] = c.Name
		}
	}
	return m
}()

// GRCh37HeaderContigLines returns the ##contig dictionary of the GRCh37
// primary assembly in sort order.
func GRCh37HeaderContigLines() []string {
	lines := make([]string, 0, len(grch37PrimaryContigs))
	for _, c := range grch37PrimaryContigs {
		lines = append(lines, fmt.Sprintf("##contig=<ID=%s,length=%d>", c.Name, c.Length))
	}
	return lines
}

// Contig is one entry of a primary contig dictionary.
type Contig struct {
	Name   string
	Length int64
}

// GRCh37PrimaryContigs returns the GRCh37 primary contig dictionary in sort
// order. Single source of truth for header writing and for dictionary
// compatibility checks (docs/CERTIFICATE.md); callers must not re-list these
// contigs.
func GRCh37PrimaryContigs() []Contig {
	out := make([]Contig, 0, len(grch37PrimaryContigs))
	for _, c := range grch37PrimaryContigs {
		out = append(out, Contig{Name: c.Name, Length: c.Length})
	}
	return out
}

var grch37ToUCSC = func() map[string]string {
	m := make(map[string]string, len(grch37PrimaryContigs)-1)
	for _, c := range grch37PrimaryContigs {
		if c.Name != "MT" {
			m[c.Name] = "chr" + c.Name
		}
	}
	return m
}()

// GRCh37ToUCSC maps a GRCh37 primary contig name (1-22, X, Y) back to the
// UCSC chr-prefixed name the SRC_CHROM annotation carries. ok is false for
// names outside the rename map (MT is never renamed; the profile rejects it
// earlier). Used by the conversion ledger to normalize naming before
// classifying (docs/LEDGER.md, tree step 1).
func GRCh37ToUCSC(name string) (string, bool) {
	s, ok := grch37ToUCSC[name]
	return s, ok
}

// grch38PrimaryContigs is the GRCh38 primary contig dictionary in sort order.
// Lengths are sequence-identical to UCSC hg38 for 1-22/X/Y; chrM and the
// GRCh38 MT are both the rCRS (16569), so MT carries over from hg38 chrM by
// renaming alone — unlike the hg19 chrM case (NC_001807, 16571).
var grch38PrimaryContigs = []struct {
	Name   string
	Length int64
}{
	{"1", 248956422}, {"2", 242193529}, {"3", 198295559}, {"4", 190214555},
	{"5", 181538259}, {"6", 170805979}, {"7", 159345973}, {"8", 145138636},
	{"9", 138394717}, {"10", 133797422}, {"11", 135086622}, {"12", 133275309},
	{"13", 114364328}, {"14", 107043718}, {"15", 101991189}, {"16", 90338345},
	{"17", 83257441}, {"18", 80373285}, {"19", 58617616}, {"20", 64444167},
	{"21", 46709983}, {"22", 50818468}, {"X", 156040895}, {"Y", 57227415},
	{"MT", 16569},
}

// GRCh38PrimaryContigs returns the GRCh38 primary contig dictionary in sort
// order. Single source of truth for the grch38-primary header dictionary and
// dictionary compatibility checks; callers must not re-list these contigs.
func GRCh38PrimaryContigs() []Contig {
	out := make([]Contig, 0, len(grch38PrimaryContigs))
	for _, c := range grch38PrimaryContigs {
		out = append(out, Contig{Name: c.Name, Length: c.Length})
	}
	return out
}

func grch38HeaderContigLines() []string {
	lines := make([]string, 0, len(grch38PrimaryContigs))
	for _, c := range grch38PrimaryContigs {
		lines = append(lines, fmt.Sprintf("##contig=<ID=%s,length=%d>", c.Name, c.Length))
	}
	return lines
}

var ucscToGRCh38 = func() map[string]string {
	m := make(map[string]string, len(grch38PrimaryContigs))
	for _, c := range grch38PrimaryContigs {
		if c.Name == "MT" {
			m["chrM"] = "MT"
			continue
		}
		m["chr"+c.Name] = c.Name
	}
	return m
}()

var grch38ToUCSC = func() map[string]string {
	m := make(map[string]string, len(grch38PrimaryContigs))
	for _, c := range grch38PrimaryContigs {
		if c.Name == "MT" {
			m["MT"] = "chrM"
			continue
		}
		m[c.Name] = "chr" + c.Name
	}
	return m
}()

// GRCh38ToUCSC maps a GRCh38 primary contig name (1-22, X, Y, MT) back to the
// UCSC chr-prefixed name the SRC_CHROM annotation carries on the reverse
// direction. Used by the conversion ledger to normalize naming before
// classifying (docs/LEDGER.md, tree step 1).
func GRCh38ToUCSC(name string) (string, bool) {
	s, ok := grch38ToUCSC[name]
	return s, ok
}

type faidxEntry struct {
	length    int64
	offset    int64
	lineBases int64
	lineWidth int64
}

// CheckREF verifies every record of a GRCh37-named plain VCF against a
// user-provided GRCh37 FASTA (with .fai). It is a pure comparison: no
// normalization, no rewriting. FASTA N bases match anything.
func CheckREF(vcfPath, fastaPath string) error {
	fai, err := loadFaidx(fastaPath + ".fai")
	if err != nil {
		return fmt.Errorf("grch37 FASTA index: %w (provide a faidx-indexed FASTA)", err)
	}
	ff, err := os.Open(fastaPath)
	if err != nil {
		return fmt.Errorf("grch37 FASTA: %w", err)
	}
	defer ff.Close()

	r, closeFn, err := OpenText(vcfPath)
	if err != nil {
		return err
	}
	defer closeFn()
	sc := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	sc.Buffer(buf, 16*1024*1024)
	checked := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			return fmt.Errorf("malformed record: %.120s", line)
		}
		e, ok := fai[f[0]]
		if !ok {
			return fmt.Errorf("grch37 FASTA has no contig %q (record %s:%s)", f[0], f[0], f[1])
		}
		pos, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil || pos < 1 {
			return fmt.Errorf("malformed POS in record: %.120s", line)
		}
		ref := strings.ToUpper(f[3])
		if pos+int64(len(ref))-1 > e.length {
			return fmt.Errorf("record %s:%s extends past GRCh37 %s length %d", f[0], f[1], f[0], e.length)
		}
		faSeq, err := fetchFASTA(ff, e, pos, int64(len(ref)))
		if err != nil {
			return err
		}
		for i := 0; i < len(ref); i++ {
			faBase := faSeq[i]
			if faBase == 'N' || ref[i] == 'N' {
				continue
			}
			if faBase != ref[i] {
				return fmt.Errorf("grch37 REF mismatch at %s:%s .. FASTA:%q vs VCF:%q — is the supplied FASTA really GRCh37?", f[0], f[1], faSeq[i:i+1], ref[i:i+1])
			}
		}
		checked++
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if checked == 0 {
		return fmt.Errorf("no records found to check in %s", vcfPath)
	}
	return nil
}

func loadFaidx(path string) (map[string]faidxEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]faidxEntry{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			return nil, fmt.Errorf("malformed .fai line: %.60s", line)
		}
		var nums [4]int64
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseInt(f[i+1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("malformed .fai line: %.60s", line)
			}
			nums[i] = v
		}
		out[f[0]] = faidxEntry{length: nums[0], offset: nums[1], lineBases: nums[2], lineWidth: nums[3]}
	}
	return out, nil
}

func fetchFASTA(f *os.File, e faidxEntry, pos, length int64) (string, error) {
	start := pos - 1
	startLine := start / e.lineBases
	endOff := start % e.lineBases
	byteStart := e.offset + startLine*e.lineWidth + endOff
	raw := make([]byte, length+(length/e.lineBases+2))
	n, err := f.ReadAt(raw, byteStart)
	if err != nil && err != io.EOF {
		return "", err
	}
	raw = raw[:n]
	var sb strings.Builder
	for i := 0; i < len(raw) && sb.Len() < int(length); i++ {
		c := raw[i]
		if c == '\n' || c == '\r' {
			continue
		}
		sb.WriteByte(c)
	}
	if sb.Len() < int(length) {
		return "", fmt.Errorf("short FASTA read at offset %d", byteStart)
	}
	return strings.ToUpper(sb.String()), nil
}

// Stats counts the profile split outcome.
type Stats struct {
	Kept             int64 `json:"kept"`
	StaleChrM        int64 `json:"stale_hg19_chrM"`
	NonPrimaryContig int64 `json:"non_primary_contig"`
}

// PreProfileRecords is the record count of the lifted output before the
// profile split; all of those records lifted successfully.
func (s Stats) PreProfileRecords() int64 { return s.Kept + s.StaleChrM + s.NonPrimaryContig }

// ProfileRejects maps reject filter ids to counts for machine-readable QC.
func (s Stats) ProfileRejects() map[string]int64 {
	m := map[string]int64{}
	if s.StaleChrM > 0 {
		m[FilterStaleHg19ChrM] = s.StaleChrM
	}
	if s.NonPrimaryContig > 0 {
		m[FilterNonPrimaryContig] = s.NonPrimaryContig
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// OpenText opens a plain or (b)gzipped VCF as text. Compression is detected
// from the gzip magic bytes so pipeline temp files without a .gz suffix work.
func OpenText(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	br := bufio.NewReader(f)
	magic, err := br.Peek(2)
	if err != nil || magic[0] != 0x1f || magic[1] != 0x8b {
		if err != nil {
			_ = f.Close()
			return nil, nil, fmt.Errorf("open %s: %w", path, err)
		}
		return br, func() { _ = f.Close() }, nil
	}
	zr, err := gzip.NewReader(br)
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	return zr, func() { _ = zr.Close(); _ = f.Close() }, nil
}

// Split reads a lifted UCSC-named VCF (plain or bgzip text) and writes:
//   - primaryW: primary-named records with the profile's primary header
//     (GRCh37 dictionary for grch37-primary/hs37d5, GRCh38 dictionary for
//     grch38-primary),
//   - rejectW: chrM records marked FILTER=stale_hg19_chrM (grch37 profiles
//     only — hg19 chrM is NC_001807, not the rCRS; on grch38-primary hg38
//     chrM carries over as MT instead) and non-primary contig records marked
//     FILTER=non_primary_contig, sharing one header.
//
// The reject writer stays empty when nothing is dropped. Records keep their
// input order, which is the pipeline's contig-sorted order.
func Split(r io.Reader, p Profile, primaryW, rejectW io.Writer) (Stats, error) {
	var stats Stats
	if !p.RenamesContigs() {
		return stats, fmt.Errorf("profile %s does not rename to primary contig naming", p)
	}
	var renameTo map[string]string
	var dictLines []string
	switch {
	case p.RenamesToGRCh37():
		renameTo = ucscToGRCh37
		dictLines = GRCh37HeaderContigLines()
	case p == GRCh38Primary:
		renameTo = ucscToGRCh38
		dictLines = grch38HeaderContigLines()
	}
	sc := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	sc.Buffer(buf, 16*1024*1024)

	var meta []string        // non-contig ## lines, original order
	var contigLines []string // original ##contig lines (reject bucket only)
	var chromLine string
	headerDone := false

	flushHeader := func(w io.Writer, extraMeta []string, dict []string) error {
		if chromLine == "" {
			return fmt.Errorf("VCF header has no #CHROM line")
		}
		var b strings.Builder
		for _, l := range meta {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		for _, l := range extraMeta {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		if dict != nil {
			b.WriteString("##vcflift_target_profile=" + string(p) + "\n")
			if p == HS37D5 {
				b.WriteString("##vcflift_profile_note=hs37d5 naming profile: GRCh37 primary contigs only; hs37d5 decoy contigs are not produced\n")
			}
			for _, l := range dict {
				b.WriteString(l)
				b.WriteByte('\n')
			}
		} else {
			for _, l := range contigLines {
				b.WriteString(l)
				b.WriteByte('\n')
			}
		}
		b.WriteString(chromLine)
		b.WriteByte('\n')
		_, err := io.WriteString(w, b.String())
		return err
	}

	rejectHeaderFlushed := false
	writeReject := func(line, filterID string) error {
		if !rejectHeaderFlushed {
			if err := flushHeader(rejectW, []string{
				"##FILTER=<ID=" + FilterStaleHg19ChrM + ",Description=\"hg19 chrM is the old NC_001807 sequence; GRCh37 MT (rCRS) coordinates cannot be derived by renaming\">",
				"##FILTER=<ID=" + FilterNonPrimaryContig + ",Description=\"Record lifted to a non-primary contig (unplaced/unlocalized/alt/haplotype); not part of the primary assembly\">",
			}, nil); err != nil {
				return err
			}
			rejectHeaderFlushed = true
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			return fmt.Errorf("malformed VCF record: %.120s", line)
		}
		f[6] = filterID
		_, err := io.WriteString(rejectW, strings.Join(f, "\t")+"\n")
		return err
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if !headerDone {
			switch {
			case strings.HasPrefix(line, "##contig="):
				contigLines = append(contigLines, line)
			case strings.HasPrefix(line, "##"):
				meta = append(meta, line)
				case strings.HasPrefix(line, "#CHROM"):
					chromLine = line
					headerDone = true
					if err := flushHeader(primaryW, nil, dictLines); err != nil {
						return stats, err
					}
			}
			continue
		}
		tab := strings.IndexByte(line, '\t')
		var contig string
		if tab < 0 {
			return stats, fmt.Errorf("malformed VCF record: %.120s", line)
		}
		contig = line[:tab]
		switch {
		case contig == "chrM" && renameTo["chrM"] != "":
			// grch38-primary: hg38 chrM is the rCRS — identical to the GRCh38
			// MT — so the record carries over by renaming, not rejection.
			f := strings.Split(line, "\t")
			f[0] = renameTo[contig]
			if _, err := io.WriteString(primaryW, strings.Join(f, "\t")+"\n"); err != nil {
				return stats, err
			}
			stats.Kept++
		case contig == "chrM" || contig == "chrMT":
			if err := writeReject(line, FilterStaleHg19ChrM); err != nil {
				return stats, err
			}
			stats.StaleChrM++
		case renameTo[contig] != "":
			f := strings.Split(line, "\t")
			f[0] = renameTo[contig]
			if _, err := io.WriteString(primaryW, strings.Join(f, "\t")+"\n"); err != nil {
				return stats, err
			}
			stats.Kept++
		default:
			if err := writeReject(line, FilterNonPrimaryContig); err != nil {
				return stats, err
			}
			stats.NonPrimaryContig++
		}
	}
	if err := sc.Err(); err != nil {
		return stats, err
	}
	if !headerDone {
		return stats, fmt.Errorf("VCF header ended without a #CHROM line")
	}
	return stats, nil
}

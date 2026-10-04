// Package certificate compares a user-provided reference FASTA's contig
// dictionary against a naming profile's expected dictionary and returns a
// compatible/incompatible verdict with a first reason. It is a comparison
// only: no repair, no download, no PAR claim (PAR masking is not measured and
// is stated so in every verdict). docs/CERTIFICATE.md is the binding design;
// the words "legal", "valid" and "invalid" never appear in its output.
package certificate

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/profile"
)

const (
	VerdictCompatible   = "compatible"
	VerdictIncompatible = "incompatible"

	// StateIncompatible is the fixed state sentence printed alongside an
	// incompatible verdict: the dictionary check ran and returned
	// incompatible; only the record-level pass was skipped
	// (docs/CERTIFICATE.md Section 7).
	StateIncompatible = "dictionary check completed: incompatible; record-level REF check skipped."
)

type LengthMismatch struct {
	Contig   string `json:"contig"`
	Length   int64  `json:"length"`
	Expected int64  `json:"expected"`
}

type Extras struct {
	Decoy    int      `json:"decoy"`
	EBV      bool     `json:"ebv"`
	Other    int      `json:"other"`
	Examples []string `json:"examples,omitempty"`
}

// Report is the machine-readable verdict carried by the QC report. PARMeasured
// is a constant false until PAR masking is actually measured; the field exists
// so the exclusion is machine-readable, not just prose.
type Report struct {
	Path             string           `json:"path"`
	Verdict          string           `json:"verdict"`
	FirstReason      string           `json:"first_reason,omitempty"`
	ExpectedContigs  int              `json:"expected_contigs"`
	MatchedContigs   int              `json:"matched_contigs"`
	MissingContigs   []string         `json:"missing_contigs"`
	LengthMismatches []LengthMismatch `json:"length_mismatches"`
	ExtraContigs     Extras           `json:"extra_contigs"`
	PARMeasured      bool             `json:"par_measured"`
}

// Statement renders the exact verdict sentence block of docs/CERTIFICATE.md
// Section 6.
func (r *Report) Statement(profileID string) string {
	if r.Verdict == VerdictCompatible {
		return fmt.Sprintf("Compatible with the %s dictionary: %d primary contigs, all lengths match, MT is the rCRS (16569). PAR masking is not measured and is not part of this verdict.", profileID, r.ExpectedContigs)
	}
	return fmt.Sprintf("Incompatible with the %s dictionary: %s. The FASTA was not modified. PAR masking is not measured and is not part of this verdict.", profileID, r.FirstReason)
}

type faiContig struct {
	name   string
	length int64
}

// Compare reads <fastaPath>.fai (names and lengths only; no sequence is
// read), normalizes names through the managed chromAlias file when available,
// and compares against the expected dictionary. It returns an error only for
// unusable inputs (missing/malformed .fai, unreadable alias file); a verdict —
// including incompatible — is a result, not an error.
func Compare(fastaPath, aliasPath string, dict []profile.Contig, profileID string) (*Report, error) {
	rep := &Report{Path: fastaPath, ExpectedContigs: len(dict)}
	fai, err := loadFai(fastaPath + ".fai")
	if err != nil {
		return nil, fmt.Errorf("grch37 FASTA index: %w (provide a faidx-indexed FASTA)", err)
	}
	aliases, err := loadAliases(aliasPath)
	if err != nil {
		return nil, err
	}
	canon := canonicalizer(aliases)

	// One canonical slot per expected contig; a second FASTA contig mapping
	// to the same canonical name is an extra (strict set rule).
	seen := make(map[string]faiContig, len(fai))
	extras := make([]faiContig, 0, len(fai))
	for _, c := range fai {
		can := canon(c.name)
		if _, dup := seen[can]; dup {
			extras = append(extras, c)
			continue
		}
		seen[can] = c
	}
	expectedCanon := make(map[string]bool, len(dict))
	for _, e := range dict {
		expectedCanon[canon(e.Name)] = true
	}
	for _, c := range fai {
		if !expectedCanon[canon(c.name)] {
			extras = append(extras, c)
		}
	}

	var missing []string
	var mismatches []LengthMismatch
	matched := 0
	for _, e := range dict {
		got, ok := seen[canon(e.Name)]
		if !ok {
			missing = append(missing, e.Name)
			continue
		}
		if got.length != e.Length {
			mismatches = append(mismatches, LengthMismatch{Contig: e.Name, Length: got.length, Expected: e.Length})
			continue
		}
		matched++
	}
	rep.MatchedContigs = matched
	rep.MissingContigs = missing
	rep.LengthMismatches = mismatches

	var firstDecoy, firstOther, ebvName string
	for _, c := range extras {
		lower := strings.ToLower(c.name)
		switch {
		case strings.Contains(lower, "ebv") || lower == "nc_007605.1":
			rep.ExtraContigs.EBV = true
			if ebvName == "" {
				ebvName = c.name
			}
		case strings.Contains(lower, "decoy"):
			rep.ExtraContigs.Decoy++
			if firstDecoy == "" {
				firstDecoy = c.name
			}
		default:
			rep.ExtraContigs.Other++
			if firstOther == "" {
				firstOther = c.name
			}
		}
		if len(rep.ExtraContigs.Examples) < 3 {
			rep.ExtraContigs.Examples = append(rep.ExtraContigs.Examples, c.name)
		}
	}

	rep.Verdict, rep.FirstReason = verdict(rep, missing, mismatches, firstDecoy, firstOther, ebvName, profileID)
	return rep, nil
}

// verdict applies the frozen reason order of docs/CERTIFICATE.md Section 5:
// missing contig, MT length, other length, decoys, EBV, other extras.
func verdict(rep *Report, missing []string, mismatches []LengthMismatch, firstDecoy, firstOther, ebvName, profileID string) (string, string) {
	if len(missing) > 0 {
		return VerdictIncompatible, fmt.Sprintf("contig %s is missing from the FASTA", missing[0])
	}
	for _, m := range mismatches {
		if m.Contig == "MT" {
			return VerdictIncompatible, fmt.Sprintf("MT length %d is the old NC_001807 sequence (hg19 chrM); this dictionary carries the rCRS MT (16569)", m.Length)
		}
	}
	if len(mismatches) > 0 {
		m := mismatches[0]
		return VerdictIncompatible, fmt.Sprintf("contig %s length %d does not match the expected %d", m.Contig, m.Length, m.Expected)
	}
	if rep.ExtraContigs.Decoy > 0 {
		return VerdictIncompatible, fmt.Sprintf("%d decoy contigs present (e.g. %s); the %s profile dictionary is primary-only and does not include decoys", rep.ExtraContigs.Decoy, firstDecoy, profileID)
	}
	if rep.ExtraContigs.EBV {
		return VerdictIncompatible, fmt.Sprintf("EBV contig present (%s); the %s dictionary does not include EBV", ebvName, profileID)
	}
	if rep.ExtraContigs.Other > 0 {
		return VerdictIncompatible, fmt.Sprintf("%d unexpected contigs present (e.g. %s)", rep.ExtraContigs.Other, firstOther)
	}
	return VerdictCompatible, ""
}

func loadFai(path string) ([]faiContig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []faiContig
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			return nil, fmt.Errorf("malformed .fai line: %.60s", line)
		}
		length, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil || length <= 0 {
			return nil, fmt.Errorf("malformed .fai line: %.60s", line)
		}
		out = append(out, faiContig{name: f[0], length: length})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty FASTA index %s", path)
	}
	return out, nil
}

// loadAliases reads a UCSC chromAlias.txt (first field canonical, remaining
// fields aliases) into alias -> canonical. An empty path yields an empty map.
func loadAliases(path string) (map[string]string, error) {
	m := map[string]string{}
	if path == "" {
		return m, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		canonical := fields[0]
		m[canonical] = canonical
		for _, alias := range fields[1:] {
			m[alias] = canonical
		}
	}
	return m, sc.Err()
}

// canonicalizer returns a name-normalizing function: managed chromAlias
// entries first, then chr-prefix add/strip fallbacks (plus the M/MT special
// case), then identity within the chr-prefixed canonical space. Unknown names
// stay in that space but match nothing expected, so they surface as extras.
func canonicalizer(aliases map[string]string) func(string) string {
	return func(name string) string {
		if v, ok := aliases[name]; ok {
			return v
		}
		switch name {
		case "M", "MT", "chrM", "chrMT":
			return "chrM"
		}
		if strings.HasPrefix(name, "chr") {
			if v, ok := aliases[name[3:]]; ok {
				return v
			}
			return name
		}
		if v, ok := aliases["chr"+name]; ok {
			return v
		}
		return "chr" + name
	}
}

package vcf

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// SourceCallSummary describes the source callset relevant to DeepVariant-style
// gVCF candidate extraction. LiftoverCandidates mirrors the record-retention
// semantics of the DeepVariant candidate pipeline: every record with a called
// non-reference genotype (GT="alt"). The view step then trims unseen <*> /
// <NON_REF> alleles, without restricting sequence-resolved calls by TYPE.
type SourceCallSummary struct {
	Records            int64
	NonReferenceCalls  int64
	NonReferencePASS   int64
	LiftoverCandidates int64
}

// VariantFileSummary captures post-conversion QC without requiring another
// external tool invocation. FILTER and contig maps are optional because they
// are mainly useful for the reject VCF.
type VariantFileSummary struct {
	Records             int64
	StarAlleleRecords   int64
	NonRefAlleleRecords int64
	FilterCounts        map[string]int64
	RejectReasonCounts  map[string]int64
	ContigCounts        map[string]int64
}

// SummarizeSourceCalls scans a VCF/gVCF once and counts source non-reference
// genotype calls eligible for candidate-mode liftover. It remains the portable
// in-process reference/fallback implementation; the native converter prefers
// the BCFtools-backed summary path for large indexed gVCFs.
func SummarizeSourceCalls(path string) (SourceCallSummary, error) {
	r, closeFn, err := openVCFReader(path)
	if err != nil {
		return SourceCallSummary{}, err
	}
	defer closeFn()

	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 32*1024*1024)
	var out SourceCallSummary
	for s.Scan() {
		line := s.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		out.Records++

		_, _, filter, format, samples, ok := sourceFields(line)
		if !ok {
			return SourceCallSummary{}, fmt.Errorf("malformed VCF record while summarizing source calls")
		}
		gtIndex := formatFieldIndex(format, "GT")
		if gtIndex < 0 || !samplesHaveAltGT(samples, gtIndex) {
			continue
		}
		out.NonReferenceCalls++
		out.LiftoverCandidates++
		if filter == "PASS" {
			out.NonReferencePASS++
		}
	}
	if err := s.Err(); err != nil {
		return SourceCallSummary{}, err
	}
	return out, nil
}

// SummarizeVariantFile scans a converted/reject VCF and returns record counts,
// placeholder-allele QC, and (when distributions is true) FILTER/reject-reason
// and chromosome distributions.
func SummarizeVariantFile(path string, distributions bool) (VariantFileSummary, error) {
	r, closeFn, err := openVCFReader(path)
	if err != nil {
		return VariantFileSummary{}, err
	}
	defer closeFn()

	out := VariantFileSummary{}
	if distributions {
		out.FilterCounts = map[string]int64{}
		out.RejectReasonCounts = map[string]int64{}
		out.ContigCounts = map[string]int64{}
	}

	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for s.Scan() {
		line := s.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		chrom, alt, filter, ok := basicVariantFields(line)
		if !ok {
			return VariantFileSummary{}, fmt.Errorf("malformed VCF record while summarizing %s", path)
		}
		out.Records++
		if containsAllele(alt, "<*>") {
			out.StarAlleleRecords++
		}
		if containsAllele(alt, "<NON_REF>") {
			out.NonRefAlleleRecords++
		}
		if distributions {
			out.FilterCounts[filter]++
			out.ContigCounts[chrom]++
			for _, reason := range strings.Split(filter, ";") {
				if reason == "" || reason == "." || reason == "PASS" {
					continue
				}
				out.RejectReasonCounts[reason]++
			}
		}
	}
	if err := s.Err(); err != nil {
		return VariantFileSummary{}, err
	}
	return out, nil
}

func openVCFReader(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	var magic [2]byte
	n, readErr := io.ReadFull(f, magic[:])
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		_ = f.Close()
		return nil, func() {}, readErr
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, func() {}, err
	}
	if n < 2 || magic[0] != 0x1f || magic[1] != 0x8b {
		return f, func() { _ = f.Close() }, nil
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, func() {}, fmt.Errorf("open compressed VCF: %w", err)
	}
	return gz, func() {
		_ = gz.Close()
		_ = f.Close()
	}, nil
}

func sourceFields(line string) (ref, alt, filter, format, samples string, ok bool) {
	var tabs [9]int
	start := 0
	for i := range tabs {
		j := strings.IndexByte(line[start:], '\t')
		if j < 0 {
			return "", "", "", "", "", false
		}
		tabs[i] = start + j
		start = tabs[i] + 1
	}
	return line[tabs[2]+1 : tabs[3]],
		line[tabs[3]+1 : tabs[4]],
		line[tabs[5]+1 : tabs[6]],
		line[tabs[7]+1 : tabs[8]],
		line[tabs[8]+1:], true
}

func basicVariantFields(line string) (chrom, alt, filter string, ok bool) {
	var tabs [7]int
	start := 0
	for i := range tabs {
		j := strings.IndexByte(line[start:], '\t')
		if j < 0 {
			return "", "", "", false
		}
		tabs[i] = start + j
		start = tabs[i] + 1
	}
	return line[:tabs[0]], line[tabs[3]+1 : tabs[4]], line[tabs[5]+1 : tabs[6]], true
}

func formatFieldIndex(format, want string) int {
	idx := 0
	for {
		j := strings.IndexByte(format, ':')
		field := format
		if j >= 0 {
			field = format[:j]
		}
		if field == want {
			return idx
		}
		if j < 0 {
			return -1
		}
		format = format[j+1:]
		idx++
	}
}

func samplesHaveAltGT(samples string, gtIndex int) bool {
	for {
		j := strings.IndexByte(samples, '\t')
		sample := samples
		if j >= 0 {
			sample = samples[:j]
		}
		if genotypeHasAlt(colonField(sample, gtIndex)) {
			return true
		}
		if j < 0 {
			return false
		}
		samples = samples[j+1:]
	}
}

func colonField(s string, want int) string {
	for idx := 0; ; idx++ {
		j := strings.IndexByte(s, ':')
		field := s
		if j >= 0 {
			field = s[:j]
		}
		if idx == want {
			return field
		}
		if j < 0 {
			return ""
		}
		s = s[j+1:]
	}
}

func genotypeHasAlt(gt string) bool {
	if gt == "" || gt == "." {
		return false
	}
	start := 0
	for start < len(gt) {
		end := start
		for end < len(gt) && gt[end] != '/' && gt[end] != '|' {
			end++
		}
		allele := gt[start:end]
		if allele != "" && allele != "." && allele != "0" {
			// GT allele indices are non-negative integers; any non-zero token is
			// therefore a non-reference genotype allele.
			allDigits := true
			for i := 0; i < len(allele); i++ {
				if allele[i] < '0' || allele[i] > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return true
			}
		}
		if end == len(gt) {
			break
		}
		start = end + 1
	}
	return false
}

func containsAllele(alt, want string) bool {
	for {
		j := strings.IndexByte(alt, ',')
		allele := alt
		if j >= 0 {
			allele = alt[:j]
		}
		if allele == want {
			return true
		}
		if j < 0 {
			return false
		}
		alt = alt[j+1:]
	}
}

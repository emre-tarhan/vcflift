package vcf

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/model"
)

var (
	contigRe        = regexp.MustCompile(`^##contig=<ID=([^,>]+)(?:,length=([0-9]+))?`)
	refSeqPrimaryRe = regexp.MustCompile(`^NC_0000([0-2][0-9])\.[0-9]+$`)
)

// Inspect reads only enough of a VCF/gVCF to classify it and infer the most
// likely assembly. It does not trust the filename extension.
func Inspect(path string) (model.Inspection, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.Inspection{}, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return model.Inspection{}, fmt.Errorf("open gzip/BGZF stream: %w", err)
		}
		defer gz.Close()
		r = gz
	}

	out := model.Inspection{Path: path, Kind: model.FileKindUnknown, Assembly: model.AssemblyUnknown}
	out.InputIndexPath, out.InputIndexKind = DiscoverSidecarIndex(path)
	contigLengths := map[string]int64{}
	seenUCSC, seenGRCh := false, false

	s := bufio.NewScanner(r)
	// Headers and gVCF records can be wide because of FORMAT/sample columns.
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)

	dataRecords := 0
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "##") {
			switch {
			case strings.HasPrefix(line, "##reference="):
				out.HeaderReference = strings.TrimPrefix(line, "##reference=")
			case strings.HasPrefix(line, "##GVCFBlock"):
				out.HasGVCFBlocks = true
			case strings.HasPrefix(line, "##ALT=<ID=NON_REF"):
				out.HasNonRefAllele = true
			case strings.HasPrefix(line, "##DeepVariant_version="):
				out.DeepVariantVersion = strings.TrimPrefix(line, "##DeepVariant_version=")
			case strings.HasPrefix(line, "##INFO=<ID=END"):
				out.HasEndInfo = true
			}

			if m := contigRe.FindStringSubmatch(line); m != nil {
				name := m[1]
				if strings.HasPrefix(name, "chr") {
					seenUCSC = true
				} else if isPrimaryGRChName(name) {
					seenGRCh = true
				}
				if m[2] != "" {
					n, _ := strconv.ParseInt(m[2], 10, 64)
					contigLengths[name] = n
				}
			}
			continue
		}

		if strings.HasPrefix(line, "#CHROM") {
			fields := strings.Split(line, "\t")
			if len(fields) > 9 {
				out.Samples = append(out.Samples, fields[9:]...)
			}
			continue
		}
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		dataRecords++
		fields := strings.Split(line, "\t")
		if len(fields) >= 8 {
			alt, info := fields[4], fields[7]
			hasNonRef := strings.Contains(alt, "<NON_REF>")
			hasStar := strings.Contains(alt, "<*>")
			if hasNonRef {
				out.HasNonRefAllele = true
			}
			if hasStar {
				out.HasStarAllele = true
			}
			if (hasNonRef || hasStar) && hasInfoKey(info, "END") {
				out.HasReferenceBlock = true
			}
		}
		// Classification does not require a whole-genome scan.
		if dataRecords >= 5000 {
			break
		}
	}
	if err := s.Err(); err != nil {
		return model.Inspection{}, fmt.Errorf("read VCF: %w", err)
	}

	switch {
	case seenUCSC && seenGRCh:
		out.ContigStyle = model.ContigStyleMixed
	case seenUCSC:
		out.ContigStyle = model.ContigStyleUCSC
	case seenGRCh:
		out.ContigStyle = model.ContigStyleGRCh
	default:
		out.ContigStyle = model.ContigStyleUnknown
	}

	out.Assembly = inferAssembly(contigLengths, out.HeaderReference)
	switch {
	case out.DeepVariantVersion != "":
		out.GVCFDialect = model.GVCFDialectDeepVariant
	case out.HasNonRefAllele && out.HasStarAllele:
		out.GVCFDialect = model.GVCFDialectMixed
	case out.HasNonRefAllele:
		out.GVCFDialect = model.GVCFDialectGATK
	case out.HasStarAllele:
		out.GVCFDialect = model.GVCFDialectStar
	default:
		out.GVCFDialect = model.GVCFDialectUnknown
	}
	if out.HasGVCFBlocks || out.HasNonRefAllele || out.HasStarAllele || out.HasReferenceBlock {
		out.Kind = model.FileKindGVCF
	} else {
		out.Kind = model.FileKindVCF
	}

	if out.Kind == model.FileKindGVCF {
		out.Notes = append(out.Notes, "gVCF reference-confidence semantics detected")
		out.Notes = append(out.Notes, "gVCF dialect: "+string(out.GVCFDialect))
		if out.DeepVariantVersion != "" {
			out.Notes = append(out.Notes, "DeepVariant "+out.DeepVariantVersion+" header detected")
		}
		if out.InputIndexPath != "" {
			out.Notes = append(out.Notes, "adjacent "+out.InputIndexKind+" index detected; it will be validated before reuse")
		}
		if out.HasReferenceBlock {
			out.Notes = append(out.Notes, "reference blocks with INFO/END detected")
		}
	}
	if out.ContigStyle == model.ContigStyleMixed {
		out.Notes = append(out.Notes, "mixed chromosome naming requires manual review")
	}
	return out, nil
}

func hasInfoKey(info, key string) bool {
	for _, item := range strings.Split(info, ";") {
		if item == key || strings.HasPrefix(item, key+"=") {
			return true
		}
	}
	return false
}

func isPrimaryGRChName(s string) bool {
	if s == "X" || s == "Y" || s == "MT" || s == "M" || s == "NC_012920.1" {
		return true
	}
	n, err := strconv.Atoi(s)
	if err == nil && n >= 1 && n <= 22 {
		return true
	}
	if m := refSeqPrimaryRe.FindStringSubmatch(s); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n >= 1 && n <= 24
	}
	return false
}

var hg38PrimaryLengths = map[string]int64{
	"1": 248956422, "2": 242193529, "3": 198295559, "4": 190214555,
	"5": 181538259, "6": 170805979, "7": 159345973, "8": 145138636,
	"9": 138394717, "10": 133797422, "11": 135086622, "12": 133275309,
	"13": 114364328, "14": 107043718, "15": 101991189, "16": 90338345,
	"17": 83257441, "18": 80373285, "19": 58617616, "20": 64444167,
	"21": 46709983, "22": 50818468, "X": 156040895, "Y": 57227415, "M": 16569,
}

var hg19PrimaryLengths = map[string]int64{
	"1": 249250621, "2": 243199373, "3": 198022430, "4": 191154276,
	"5": 180915260, "6": 171115067, "7": 159138663, "8": 146364022,
	"9": 141213431, "10": 135534747, "11": 135006516, "12": 133851895,
	"13": 115169878, "14": 107349540, "15": 102531392, "16": 90354753,
	"17": 81195210, "18": 78077248, "19": 59128983, "20": 63025520,
	"21": 48129895, "22": 51304566, "X": 155270560, "Y": 59373566, "M": 16571,
}

func inferAssembly(lengths map[string]int64, headerReference string) model.Assembly {
	match38, match19 := 0, 0
	for name, length := range lengths {
		canonical := canonicalPrimaryContig(name)
		if canonical == "" {
			continue
		}
		if want, ok := hg38PrimaryLengths[canonical]; ok && want == length {
			match38++
		}
		if want, ok := hg19PrimaryLengths[canonical]; ok && want == length {
			match19++
		}
	}
	if match38 > 0 && match19 == 0 {
		return model.AssemblyHG38
	}
	if match19 > 0 && match38 == 0 {
		return model.AssemblyHG19
	}

	lower := strings.ToLower(headerReference)
	if strings.Contains(lower, "grch38") || strings.Contains(lower, "hg38") {
		return model.AssemblyHG38
	}
	if strings.Contains(lower, "grch37") || strings.Contains(lower, "hg19") {
		return model.AssemblyHG19
	}
	return model.AssemblyUnknown
}

func canonicalPrimaryContig(name string) string {
	s := strings.TrimPrefix(name, "chr")
	switch s {
	case "X", "Y":
		return s
	case "M", "MT":
		return "M"
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 22 {
		return strconv.Itoa(n)
	}
	if name == "NC_012920.1" {
		return "M"
	}
	if m := refSeqPrimaryRe.FindStringSubmatch(name); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		switch {
		case n >= 1 && n <= 22:
			return strconv.Itoa(n)
		case n == 23:
			return "X"
		case n == 24:
			return "Y"
		}
	}
	return ""
}

package rejects

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Audit struct {
	Path             string                      `json:"path"`
	Records          int64                       `json:"records"`
	Filters          map[string]int64            `json:"filters"`
	Types            map[string]int64            `json:"types"`
	FilterTypes      map[string]map[string]int64 `json:"filter_types"`
	Contigs          map[string]int64            `json:"contigs"`
	AlleleLengthBins map[string]int64            `json:"max_allele_length_bins"`
	MaxAlleleLength  int                         `json:"max_allele_length"`
}

func Analyze(path string) (Audit, error) {
	a := Audit{
		Path: path, Filters: map[string]int64{}, Types: map[string]int64{},
		FilterTypes: map[string]map[string]int64{}, Contigs: map[string]int64{},
		AlleleLengthBins: map[string]int64{},
	}
	r, closeFn, err := openText(path)
	if err != nil {
		return a, err
	}
	defer closeFn()
	sc := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	sc.Buffer(buf, 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			return a, fmt.Errorf("malformed VCF record at %s", linePrefix(line, 120))
		}
		ref := f[3]
		alts := strings.Split(f[4], ",")
		filter := f[6]
		typ := classify(ref, alts)
		a.Records++
		a.Filters[filter]++
		a.Types[typ]++
		if a.FilterTypes[filter] == nil {
			a.FilterTypes[filter] = map[string]int64{}
		}
		a.FilterTypes[filter][typ]++
		a.Contigs[f[0]]++
		maxLen := len(ref)
		for _, alt := range alts {
			if symbolic(alt) {
				continue
			}
			if len(alt) > maxLen {
				maxLen = len(alt)
			}
		}
		if maxLen > a.MaxAlleleLength {
			a.MaxAlleleLength = maxLen
		}
		a.AlleleLengthBins[lengthBin(maxLen)]++
	}
	if err := sc.Err(); err != nil {
		return a, err
	}
	return a, nil
}

func TopContigs(m map[string]int64, n int) []string {
	type pair struct {
		k string
		v int64
	}
	p := make([]pair, 0, len(m))
	for k, v := range m {
		p = append(p, pair{k, v})
	}
	sort.Slice(p, func(i, j int) bool {
		if p[i].v == p[j].v {
			return p[i].k < p[j].k
		}
		return p[i].v > p[j].v
	})
	if n > len(p) {
		n = len(p)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = p[i].k + ":" + strconv.FormatInt(p[i].v, 10)
	}
	return out
}

func classify(ref string, alts []string) string {
	seq := 0
	allSNP := len(ref) == 1
	allIndel := true
	for _, alt := range alts {
		if symbolic(alt) {
			continue
		}
		seq++
		if len(alt) != 1 || len(ref) != 1 {
			allSNP = false
		}
		if len(alt) == len(ref) {
			allIndel = false
		}
	}
	if seq == 0 {
		return "SYMBOLIC"
	}
	if allSNP {
		return "SNP"
	}
	if allIndel {
		return "INDEL"
	}
	return "MIXED_OTHER"
}

func symbolic(a string) bool {
	return a == "*" || (strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">"))
}

func lengthBin(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 2:
		return "2"
	case n <= 5:
		return "3-5"
	case n <= 10:
		return "6-10"
	case n <= 20:
		return "11-20"
	case n <= 50:
		return "21-50"
	case n <= 100:
		return "51-100"
	case n <= 200:
		return "101-200"
	case n <= 500:
		return "201-500"
	case n <= 1000:
		return "501-1000"
	default:
		return ">1000"
	}
}

func openText(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	closeFn := func() { _ = f.Close() }
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			_ = f.Close()
			return nil, func() {}, err
		}
		return gz, func() { _ = gz.Close(); _ = f.Close() }, nil
	}
	return f, closeFn, nil
}

func linePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

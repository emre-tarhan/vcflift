package vcf

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// CountRecords counts non-header VCF records. gzip.Reader also accepts BGZF,
// so this works for normal .vcf.gz and bgzip-compressed output.
func CountRecords(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return 0, fmt.Errorf("open compressed VCF: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 32*1024*1024)
	var n int64
	for s.Scan() {
		line := s.Text()
		if line != "" && line[0] != '#' {
			n++
		}
	}
	if err := s.Err(); err != nil {
		return 0, err
	}
	return n, nil
}

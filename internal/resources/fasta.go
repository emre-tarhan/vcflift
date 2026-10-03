package resources

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func prepareGzipFASTA(srcGZ, dstFASTA string) error {
	in, err := os.Open(srcGZ)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("open FASTA gzip: %w", err)
	}
	defer gz.Close()

	partial := dstFASTA + ".part"
	_ = os.Remove(partial)
	out, err := os.Create(partial)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, gz)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("decompress FASTA: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(partial)
		return closeErr
	}
	if err := os.Rename(partial, dstFASTA); err != nil {
		_ = os.Remove(partial)
		return err
	}
	return ensureReferenceSidecars(dstFASTA)
}

// buildFAI creates a standard samtools-compatible .fai without requiring a
// second native dependency. UCSC/Ensembl reference FASTAs use fixed-width
// sequence lines, which is exactly what FAI random access expects.
func buildFAI(fastaPath, faiPath string) error {
	f, err := os.Open(fastaPath)
	if err != nil {
		return err
	}
	defer f.Close()

	tmp := faiPath + ".part"
	_ = os.Remove(tmp)
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(out)

	type seq struct {
		name      string
		length    int64
		offset    int64
		lineBases int
		lineWidth int
		lastBases int
	}
	var cur *seq
	var fileOffset int64

	flush := func() error {
		if cur == nil {
			return nil
		}
		if cur.lineBases == 0 && cur.length > 0 {
			return fmt.Errorf("cannot index FASTA sequence %s", cur.name)
		}
		_, err := fmt.Fprintf(bw, "%s\t%d\t%d\t%d\t%d\n", cur.name, cur.length, cur.offset, cur.lineBases, cur.lineWidth)
		return err
	}

	br := bufio.NewReaderSize(f, 1<<20)
	for {
		raw, readErr := br.ReadString('\n')
		if len(raw) > 0 {
			lineBytes := len(raw)
			line := strings.TrimSuffix(raw, "\n")
			line = strings.TrimSuffix(line, "\r")
			if strings.HasPrefix(line, ">") {
				if err := flush(); err != nil {
					_ = out.Close()
					_ = os.Remove(tmp)
					return err
				}
				name := strings.Fields(strings.TrimPrefix(line, ">"))
				if len(name) == 0 {
					_ = out.Close()
					_ = os.Remove(tmp)
					return fmt.Errorf("FASTA header without sequence name at byte %d", fileOffset)
				}
				cur = &seq{name: name[0], offset: fileOffset + int64(lineBytes)}
			} else if cur != nil && len(line) > 0 {
				bases := len(line)
				if cur.lineBases == 0 {
					cur.lineBases = bases
					cur.lineWidth = lineBytes
				} else if cur.lastBases != 0 && cur.lastBases != cur.lineBases {
					_ = out.Close()
					_ = os.Remove(tmp)
					return fmt.Errorf("variable-width FASTA lines in %s", cur.name)
				}
				cur.length += int64(bases)
				cur.lastBases = bases
			}
			fileOffset += int64(lineBytes)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			return readErr
		}
	}
	if err := flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := bw.Flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(faiPath), 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, faiPath)
}

func referenceDictPath(fastaPath string) string {
	ext := filepath.Ext(fastaPath)
	if ext == "" {
		return fastaPath + ".dict"
	}
	return strings.TrimSuffix(fastaPath, ext) + ".dict"
}

// buildSequenceDictionaryFromFAI creates the minimal SAM sequence dictionary
// required by GATK/Picard from the already verified FASTA index. Sequence names
// and lengths are copied verbatim from the source reference.
func buildSequenceDictionaryFromFAI(faiPath, dictPath string) error {
	in, err := os.Open(faiPath)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dictPath + ".part"
	_ = os.Remove(tmp)
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(out)
	if _, err := fmt.Fprintln(bw, "@HD\tVN:1.6\tSO:unsorted"); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}

	s := bufio.NewScanner(in)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 1024*1024)
	for s.Scan() {
		fields := strings.Split(s.Text(), "\t")
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			_ = out.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("invalid FAI line: %q", s.Text())
		}
		if _, err := fmt.Fprintf(bw, "@SQ\tSN:%s\tLN:%s\n", fields[0], fields[1]); err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := s.Err(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := bw.Flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dictPath)
}

func ensureReferenceSidecars(fastaPath string) error {
	fai := fastaPath + ".fai"
	if !regularFile(fai) {
		if err := buildFAI(fastaPath, fai); err != nil {
			return err
		}
	}
	dict := referenceDictPath(fastaPath)
	if !regularFile(dict) {
		if err := buildSequenceDictionaryFromFAI(fai, dict); err != nil {
			return err
		}
	}
	return nil
}

package resources

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildRenameMap converts UCSC chromAlias.txt into the two-column format
// accepted by `bcftools annotate --rename-chrs`: <old> <new>. The canonical
// UCSC name is the first column of chromAlias.txt; every alias maps to it.
func BuildRenameMap(aliasPath, outPath string) error {
	in, err := os.Open(aliasPath)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	partial := outPath + ".part"
	out, err := os.Create(partial)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(out)
	seen := map[string]bool{}
	s := bufio.NewScanner(in)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		canonical := fields[0]
		for _, alias := range fields[1:] {
			if alias == canonical || seen[alias] {
				continue
			}
			if _, err := fmt.Fprintf(bw, "%s\t%s\n", alias, canonical); err != nil {
				_ = out.Close()
				_ = os.Remove(partial)
				return err
			}
			seen[alias] = true
		}
	}
	if err := s.Err(); err != nil {
		_ = out.Close()
		_ = os.Remove(partial)
		return err
	}
	if err := bw.Flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(partial)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(partial)
		return err
	}
	return os.Rename(partial, outPath)
}

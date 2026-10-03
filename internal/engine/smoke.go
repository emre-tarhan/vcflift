package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SmokeTest exercises the actual liftover plugin, FASTA random access, chain
// parsing, process piping, and VCF writing with a tiny identity mapping. It is
// intended for engine import/release validation, not for every conversion.
func SmokeTest(ctx context.Context, inst Installation) error {
	root, err := os.MkdirTemp("", "vcflift-engine-smoke-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	seq := "ACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGT"
	src := filepath.Join(root, "src.fa")
	dst := filepath.Join(root, "dst.fa")
	for _, p := range []string{src, dst} {
		if err := os.WriteFile(p, []byte(">chr1\n"+seq+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(p+".fai", []byte("chr1\t100\t6\t100\t101\n"), 0o644); err != nil {
			return err
		}
	}
	chain := filepath.Join(root, "identity.chain")
	if err := os.WriteFile(chain, []byte("chain 1000 chr1 100 + 0 100 chr1 100 + 0 100 1\n100\n"), 0o644); err != nil {
		return err
	}
	input := filepath.Join(root, "input.vcf")
	vcf := "##fileformat=VCFv4.2\n##contig=<ID=chr1,length=100>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nchr1\t2\t.\tC\tT\t60\tPASS\t.\n"
	if err := os.WriteFile(input, []byte(vcf), 0o644); err != nil {
		return err
	}
	output := filepath.Join(root, "output.vcf")
	reject := filepath.Join(root, "reject.vcf")
	p := Pipeline{
		Env: map[string]string{"BCFTOOLS_PLUGINS": inst.PluginDir},
		Steps: []Step{
			{Name: "engine smoke liftover", Executable: inst.BCFTools, Args: []string{
				"+liftover", "-Ou", input, "--",
				"-s", src, "-f", dst, "-c", chain,
				"--reject", reject, "--write-src", "--write-reject",
			}, PipeToNext: true},
			{Name: "engine smoke sort", Executable: inst.BCFTools, Args: []string{"sort", "-Ov", "-o", output}, PipeToNext: false},
		},
	}
	if err := (Runner{}).Run(ctx, p, nil); err != nil {
		return fmt.Errorf("native engine liftover smoke test failed: %w", err)
	}
	f, err := os.Open(output)
	if err != nil {
		return fmt.Errorf("native engine smoke output missing: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) >= 5 && cols[0] == "chr1" && cols[1] == "2" && cols[3] == "C" && cols[4] == "T" {
			return nil
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	return fmt.Errorf("native engine smoke test produced no expected chr1:2 C>T record")
}

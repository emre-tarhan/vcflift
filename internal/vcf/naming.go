package vcf

import (
	"path/filepath"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/model"
)

func DefaultOutputPath(input string, mode model.ConversionMode) string {
	dir := filepath.Dir(input)
	base := filepath.Base(input)
	lower := strings.ToLower(base)

	strip := func(suffix string) (string, bool) {
		if strings.HasSuffix(lower, suffix) {
			return base[:len(base)-len(suffix)], true
		}
		return "", false
	}

	stem := base
	for _, suffix := range []string{".gvcf.vcf.gz", ".g.vcf.gz", ".vcf.gz", ".vcf"} {
		if s, ok := strip(suffix); ok {
			stem = s
			break
		}
	}

	if mode == model.ModeGVCFPreserveExperimental {
		return filepath.Join(dir, stem+".hg19.g.vcf.gz")
	}
	return filepath.Join(dir, stem+".hg19.vcf.gz")
}

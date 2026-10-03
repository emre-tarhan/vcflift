package vcf

import (
	"os"
	"strings"
)

// DiscoverSidecarIndex returns a standard adjacent VCF index if one exists.
// BGZF-compressed VCF/gVCF files conventionally use <file>.tbi or <file>.csi.
func DiscoverSidecarIndex(path string) (indexPath, kind string) {
	for _, suffix := range []string{".tbi", ".csi"} {
		candidate := path + suffix
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			return candidate, strings.TrimPrefix(suffix, ".")
		}
	}
	return "", ""
}

// SidecarIndexFresh rejects an obviously stale index when the variant file has
// been modified more recently. A fresh timestamp is not proof of validity;
// callers should still ask bcftools to read the index before reuse.
func SidecarIndexFresh(variantPath, indexPath string) bool {
	variantInfo, err := os.Stat(variantPath)
	if err != nil {
		return false
	}
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		return false
	}
	return !indexInfo.ModTime().Before(variantInfo.ModTime())
}

package engine

import (
	"context"
	"os/exec"

	"github.com/emre-tarhan/vcflift/internal/subprocess"
)

// ValidateVariantIndex asks bcftools to read the existing adjacent index for a
// compressed VCF/gVCF. A false result means callers should rebuild/stage an
// index rather than trusting the sidecar.
func ValidateVariantIndex(ctx context.Context, bcftools, variantPath string) bool {
	if bcftools == "" || variantPath == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, bcftools, "index", "-n", variantPath)
	subprocess.HideConsole(cmd)
	return cmd.Run() == nil
}

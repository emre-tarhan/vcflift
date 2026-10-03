package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateVariantIndex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	d := t.TempDir()
	fake := filepath.Join(d, "bcftools")
	if err := os.WriteFile(fake, []byte(`#!/bin/sh
if [ "$1" = "index" ] && [ "$2" = "-n" ] && [ -f "$3.tbi" ]; then echo 1; exit 0; fi
exit 2
`), 0o755); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(d, "sample.g.vcf.gz")
	if err := os.WriteFile(variant, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ValidateVariantIndex(context.Background(), fake, variant) {
		t.Fatal("unexpected valid index")
	}
	if err := os.WriteFile(variant+".tbi", []byte("i"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ValidateVariantIndex(context.Background(), fake, variant) {
		t.Fatal("expected valid index")
	}
}

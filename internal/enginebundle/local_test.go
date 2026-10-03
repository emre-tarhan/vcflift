package enginebundle

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/emre-tarhan/vcflift/internal/engine"
)

func TestPackLocalCreatesInstallableBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake bcftools fixture is Unix-only")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	plugins := filepath.Join(root, "plugins")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	bcf := filepath.Join(bin, "bcftools")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "bcftools 1.24"; exit 0; fi
if [ "$1" = "plugin" ] && [ "$2" = "-l" ]; then echo "liftover"; exit 0; fi
if [ "$1" = "+liftover" ] && [ "$2" = "-h" ]; then echo "--write-src --write-reject --lift-end"; exit 0; fi
exit 0
`
	if err := os.WriteFile(bcf, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins, "liftover.so"), []byte("plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "engine.zip")
	packed, err := PackLocal(context.Background(), PackOptions{
		Installation: engine.Installation{BCFTools: bcf, PluginDir: plugins},
		OutputZip:    bundle,
		ScoreRef:     "test-score-ref",
	})
	if err != nil {
		t.Fatal(err)
	}
	if packed.Manifest.BCFToolsVer != "bcftools 1.24" {
		t.Fatalf("version=%q", packed.Manifest.BCFToolsVer)
	}
	zr, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	seenManifest, seenBCF, seenPlugin := false, false, false
	for _, f := range zr.File {
		switch f.Name {
		case "manifest.json":
			seenManifest = true
		case "bin/bcftools":
			seenBCF = true
		case "plugins/liftover.so":
			seenPlugin = true
		}
	}
	if !seenManifest || !seenBCF || !seenPlugin {
		t.Fatalf("bundle entries missing: manifest=%v bcf=%v plugin=%v", seenManifest, seenBCF, seenPlugin)
	}
}

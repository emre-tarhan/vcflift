package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSmokeTestExercisesPipeline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is Unix-only")
	}
	root := t.TempDir()
	bcf := filepath.Join(root, "bcftools")
	script := `#!/bin/sh
if [ "$1" = "+liftover" ]; then
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -Ou|-Ob|-Ov|-Oz) shift ;;
      --) break ;;
      *) cat "$1"; exit 0 ;;
    esac
  done
  exit 1
fi
if [ "$1" = "sort" ]; then
  out=""
  prev=""
  for arg in "$@"; do
    if [ "$prev" = "-o" ]; then out="$arg"; break; fi
    prev="$arg"
  done
  cat > "$out"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(bcf, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := filepath.Join(root, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SmokeTest(context.Background(), Installation{BCFTools: bcf, PluginDir: plugins}); err != nil {
		t.Fatal(err)
	}
}

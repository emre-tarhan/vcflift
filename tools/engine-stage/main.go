package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/emre-tarhan/vcflift/internal/enginebundle"
)

func main() {
	bundle := flag.String("bundle", "", "VCF Lift engine bundle zip")
	payloadRoot := flag.String("payload-root", "internal/enginebundle/payload", "embedded payload root")
	flag.Parse()
	if *bundle == "" {
		fatal("--bundle is required")
	}

	tmp, err := os.MkdirTemp("", "vcflift-engine-stage-*")
	if err != nil {
		fatal(err.Error())
	}
	defer os.RemoveAll(tmp)
	m := enginebundle.NewManager(filepath.Join(tmp, "cache"))
	inst, manifest, err := m.InstallArchive(*bundle)
	if err != nil {
		fatal(err.Error())
	}
	installedRoot := filepath.Dir(filepath.Dir(inst.BCFTools))
	dst := filepath.Join(*payloadRoot, manifest.Platform)
	if err := os.RemoveAll(dst); err != nil {
		fatal(err.Error())
	}
	if err := copyTree(installedRoot, dst); err != nil {
		fatal(err.Error())
	}
	fmt.Printf("staged %s engine %s into %s\n", manifest.Platform, manifest.EngineVersion, dst)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, cpErr := io.Copy(f, in)
		closeErr := f.Close()
		if cpErr != nil {
			return cpErr
		}
		return closeErr
	})
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "engine-stage:", msg)
	os.Exit(1)
}

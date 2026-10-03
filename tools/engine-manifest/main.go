package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode,omitempty"`
}

type manifest struct {
	FormatVersion int         `json:"format_version"`
	EngineVersion string      `json:"engine_version"`
	Platform      string      `json:"platform"`
	BCFTools      string      `json:"bcftools"`
	PluginDir     string      `json:"plugin_dir"`
	BCFToolsVer   string      `json:"bcftools_version"`
	ScoreRef      string      `json:"score_ref"`
	Files         []fileEntry `json:"files"`
}

func main() {
	var root, platform, engineVersion, bcftoolsPath, pluginDir, bcftoolsVersion, scoreRef, output string
	flag.StringVar(&root, "root", "", "staged engine directory")
	flag.StringVar(&platform, "platform", "", "GOOS-GOARCH platform key")
	flag.StringVar(&engineVersion, "engine-version", "", "engine bundle version")
	flag.StringVar(&bcftoolsPath, "bcftools", "", "relative path to bcftools executable")
	flag.StringVar(&pluginDir, "plugin-dir", "plugins", "relative plugin directory")
	flag.StringVar(&bcftoolsVersion, "bcftools-version", "", "recorded bcftools version")
	flag.StringVar(&scoreRef, "score-ref", "", "freeseek/score git ref/commit")
	flag.StringVar(&output, "output", "", "output manifest path (default root/manifest.json)")
	flag.Parse()
	if root == "" || platform == "" || engineVersion == "" || bcftoolsPath == "" || bcftoolsVersion == "" || scoreRef == "" {
		fatal("--root, --platform, --engine-version, --bcftools, --bcftools-version and --score-ref are required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		fatal(err.Error())
	}
	if output == "" {
		output = filepath.Join(rootAbs, "manifest.json")
	}

	var files []fileEntry
	err = filepath.WalkDir(rootAbs, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if samePath(p, output) {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, p)
		if err != nil {
			return err
		}
		hash, err := sha256File(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, fileEntry{Path: filepath.ToSlash(rel), SHA256: hash, Mode: uint32(info.Mode().Perm())})
		return nil
	})
	if err != nil {
		fatal(err.Error())
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	m := manifest{
		FormatVersion: 1,
		EngineVersion: engineVersion,
		Platform:      platform,
		BCFTools:      filepath.ToSlash(bcftoolsPath),
		PluginDir:     filepath.ToSlash(pluginDir),
		BCFToolsVer:   bcftoolsVersion,
		ScoreRef:      scoreRef,
		Files:         files,
	}
	if !containsFile(files, m.BCFTools) {
		fatal("bcftools path not found in staged files: " + m.BCFTools)
	}
	pluginPrefix := strings.TrimSuffix(m.PluginDir, "/") + "/"
	foundPlugin := false
	for _, f := range files {
		if strings.HasPrefix(f.Path, pluginPrefix) && strings.Contains(filepath.Base(f.Path), "liftover") {
			foundPlugin = true
			break
		}
	}
	if !foundPlugin {
		fatal("no liftover plugin found under " + m.PluginDir)
	}

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fatal(err.Error())
	}
	b = append(b, '\n')
	if err := os.WriteFile(output, b, 0o644); err != nil {
		fatal(err.Error())
	}
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func containsFile(files []fileEntry, path string) bool {
	for _, f := range files {
		if f.Path == path {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return aa == bb
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "engine-manifest:", msg)
	os.Exit(1)
}

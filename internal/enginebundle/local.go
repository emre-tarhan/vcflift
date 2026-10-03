package enginebundle

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/engine"
)

type PackOptions struct {
	Installation engine.Installation
	OutputZip    string
	ScoreRef     string
	Platform     string
	RuntimeDirs  []string
}

type PackResult struct {
	Archive  string   `json:"archive"`
	Manifest Manifest `json:"manifest"`
}

type sourceFile struct {
	Source string
	Target string
	Mode   os.FileMode
}

// PackLocal validates a native engine on the current machine, collects the
// executable, liftover plugin and adjacent runtime libraries, then writes the
// exact same manifest+payload format used by embedded release builds.
func PackLocal(ctx context.Context, opts PackOptions) (PackResult, error) {
	if opts.Platform == "" {
		opts.Platform = PlatformKey()
	}
	if opts.Platform != PlatformKey() {
		return PackResult{}, fmt.Errorf("local engine packing must run on the target platform: requested=%s runtime=%s", opts.Platform, PlatformKey())
	}
	if opts.OutputZip == "" {
		return PackResult{}, fmt.Errorf("output bundle path is empty")
	}

	inst, err := engine.ValidateInstallation(ctx, opts.Installation)
	if err != nil {
		return PackResult{}, err
	}
	if err := engine.RequireVersion(inst, engine.RequiredBCFToolsVersion); err != nil {
		return PackResult{}, err
	}
	if opts.ScoreRef == "" {
		opts.ScoreRef = "local-unverified"
	}

	files, err := collectLocalFiles(inst, opts.RuntimeDirs)
	if err != nil {
		return PackResult{}, err
	}
	entries := make([]File, 0, len(files))
	for _, f := range files {
		hash, err := sha256File(f.Source)
		if err != nil {
			return PackResult{}, err
		}
		entries = append(entries, File{Path: filepath.ToSlash(f.Target), SHA256: hash, Mode: uint32(f.Mode.Perm())})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	fingerprint := sha256.New()
	for _, e := range entries {
		_, _ = io.WriteString(fingerprint, e.Path+"\x00"+e.SHA256+"\n")
	}
	fp := hex.EncodeToString(fingerprint.Sum(nil))
	version := engine.ParseBCFToolsVersion(inst.Version)
	manifest := Manifest{
		FormatVersion: BundleFormatVersion,
		EngineVersion: fmt.Sprintf("bcftools-%s-local-%s", version, fp[:12]),
		Platform:      opts.Platform,
		BCFTools:      bcftoolsBundlePath(),
		PluginDir:     "plugins",
		BCFToolsVer:   inst.Version,
		ScoreRef:      opts.ScoreRef,
		Files:         entries,
	}
	if err := validateManifest(manifest, opts.Platform); err != nil {
		return PackResult{}, err
	}
	if err := writeBundleZip(opts.OutputZip, manifest, files); err != nil {
		return PackResult{}, err
	}
	return PackResult{Archive: opts.OutputZip, Manifest: manifest}, nil
}

func collectLocalFiles(inst engine.Installation, runtimeDirs []string) ([]sourceFile, error) {
	bcfAbs, err := filepath.Abs(inst.BCFTools)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(bcfAbs); err != nil || !st.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, fmt.Errorf("bcftools executable %s: %w", bcfAbs, err)
	}
	plugin, err := findLiftoverPlugin(inst.PluginDir)
	if err != nil {
		return nil, err
	}

	files := []sourceFile{
		{Source: bcfAbs, Target: bcftoolsBundlePath(), Mode: 0o755},
		{Source: plugin, Target: filepath.ToSlash(filepath.Join("plugins", filepath.Base(plugin))), Mode: 0o644},
	}
	seen := map[string]bool{canonical(bcfAbs): true, canonical(plugin): true}

	if runtime.GOOS == "windows" {
		dlls, err := collectWindowsRuntimeDLLs([]string{bcfAbs, plugin}, append([]string{filepath.Dir(bcfAbs), inst.PluginDir}, runtimeDirs...))
		if err != nil {
			return nil, err
		}
		for _, src := range dlls {
			if seen[canonical(src)] {
				continue
			}
			seen[canonical(src)] = true
			files = append(files, sourceFile{Source: src, Target: filepath.ToSlash(filepath.Join("bin", filepath.Base(src))), Mode: 0o644})
		}
	} else {
		// Linux source-built release engines rely on the distribution ABI. Extra
		// runtime directories are an advanced escape hatch for
		// shipping additional non-system shared objects.
		for _, dir := range runtimeDirs {
			if strings.TrimSpace(dir) == "" {
				continue
			}
			ents, err := os.ReadDir(dir)
			if err != nil {
				return nil, fmt.Errorf("read runtime directory %s: %w", dir, err)
			}
			for _, ent := range ents {
				if ent.IsDir() {
					continue
				}
				name := strings.ToLower(ent.Name())
				if filepath.Ext(name) != ".so" && !strings.Contains(name, ".so.") {
					continue
				}
				src := filepath.Join(dir, ent.Name())
				if seen[canonical(src)] {
					continue
				}
				seen[canonical(src)] = true
				files = append(files, sourceFile{Source: src, Target: filepath.ToSlash(filepath.Join("bin", ent.Name())), Mode: 0o644})
			}
		}
	}
	return files, nil
}

func collectWindowsRuntimeDLLs(roots, extraSearchDirs []string) ([]string, error) {
	searchDirs := uniqueDirs(extraSearchDirs)
	for _, item := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(item) != "" {
			searchDirs = appendUniqueDir(searchDirs, item)
		}
	}

	queue := append([]string(nil), roots...)
	visited := map[string]bool{}
	found := map[string]string{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		key := canonical(current)
		if visited[key] {
			continue
		}
		visited[key] = true
		pf, err := pe.Open(current)
		if err != nil {
			return nil, fmt.Errorf("read PE imports from %s: %w", current, err)
		}
		libs, err := pf.ImportedLibraries()
		_ = pf.Close()
		if err != nil {
			return nil, fmt.Errorf("read PE dependencies from %s: %w", current, err)
		}
		for _, lib := range libs {
			if isWindowsSystemDLL(lib) {
				continue
			}
			resolved := findLibrary(lib, searchDirs)
			if resolved == "" {
				return nil, fmt.Errorf("portable engine dependency %s required by %s was not found; add its directory with --runtime-dirs", lib, current)
			}
			if isUnderWindowsSystemDir(resolved) {
				continue
			}
			libKey := strings.ToLower(filepath.Base(resolved))
			if _, ok := found[libKey]; !ok {
				found[libKey] = resolved
				queue = append(queue, resolved)
			}
		}
	}
	out := make([]string, 0, len(found))
	for _, p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func uniqueDirs(in []string) []string {
	var out []string
	for _, dir := range in {
		out = appendUniqueDir(out, dir)
	}
	return out
}

func appendUniqueDir(in []string, dir string) []string {
	if strings.TrimSpace(dir) == "" {
		return in
	}
	clean := canonical(dir)
	for _, existing := range in {
		if canonical(existing) == clean {
			return in
		}
	}
	return append(in, dir)
}

func findLibrary(name string, dirs []string) string {
	for _, dir := range dirs {
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

func isUnderWindowsSystemDir(path string) bool {
	win := os.Getenv("WINDIR")
	if win == "" {
		return false
	}
	p := canonical(path)
	for _, dir := range []string{filepath.Join(win, "System32"), filepath.Join(win, "SysWOW64"), filepath.Join(win, "System")} {
		base := canonical(dir)
		if p == base || strings.HasPrefix(p, base+strings.ToLower(string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func isWindowsSystemDLL(name string) bool {
	n := strings.ToLower(filepath.Base(name))
	if strings.HasPrefix(n, "api-ms-win-") || strings.HasPrefix(n, "ext-ms-win-") {
		return true
	}
	_, ok := map[string]struct{}{
		"kernel32.dll": {}, "kernelbase.dll": {}, "ntdll.dll": {}, "user32.dll": {},
		"advapi32.dll": {}, "ws2_32.dll": {}, "shell32.dll": {}, "ole32.dll": {},
		"oleaut32.dll": {}, "secur32.dll": {}, "bcrypt.dll": {}, "crypt32.dll": {},
		"rpcrt4.dll": {}, "gdi32.dll": {}, "gdi32full.dll": {}, "msvcrt.dll": {},
		"ucrtbase.dll": {}, "combase.dll": {}, "imm32.dll": {}, "version.dll": {},
	}[n]
	return ok
}

func findLiftoverPlugin(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("plugin directory is empty")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read plugin directory %s: %w", dir, err)
	}
	for _, ent := range ents {
		if ent.IsDir() {
			continue
		}
		name := strings.ToLower(ent.Name())
		if name == "liftover.so" || name == "liftover.dll" || name == "liftover.dylib" {
			return filepath.Join(dir, ent.Name()), nil
		}
	}
	return "", fmt.Errorf("liftover plugin not found under %s", dir)
}

func writeBundleZip(output string, manifest Manifest, files []sourceFile) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	partial := output + ".partial"
	_ = os.Remove(partial)
	f, err := os.Create(partial)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	fail := func(err error) error {
		_ = zw.Close()
		_ = f.Close()
		_ = os.Remove(partial)
		return err
	}
	mb, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fail(err)
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return fail(err)
	}
	if _, err := mw.Write(append(mb, '\n')); err != nil {
		return fail(err)
	}
	for _, src := range files {
		h := &zip.FileHeader{Name: filepath.ToSlash(src.Target), Method: zip.Deflate}
		h.SetMode(src.Mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return fail(err)
		}
		in, err := os.Open(src.Source)
		if err != nil {
			return fail(err)
		}
		_, cpErr := io.Copy(w, in)
		closeErr := in.Close()
		if cpErr != nil {
			return fail(cpErr)
		}
		if closeErr != nil {
			return fail(closeErr)
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(partial)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(partial)
		return err
	}
	_ = os.Remove(output)
	return os.Rename(partial, output)
}

func bcftoolsBundlePath() string {
	if runtime.GOOS == "windows" {
		return "bin/bcftools.exe"
	}
	return "bin/bcftools"
}

func canonical(p string) string {
	abs, _ := filepath.Abs(p)
	return strings.ToLower(filepath.Clean(abs))
}

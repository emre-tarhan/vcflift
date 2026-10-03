package enginebundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestInstallExtractsAndReusesVerifiedBundle(t *testing.T) {
	platform := "linux-amd64"
	bcf := []byte("fake-bcftools")
	plugin := []byte("fake-plugin")
	manifest := Manifest{
		FormatVersion: BundleFormatVersion,
		EngineVersion: "bcftools-1.24-test",
		Platform:      platform,
		BCFTools:      "bin/bcftools",
		PluginDir:     "plugins",
		BCFToolsVer:   "bcftools 1.24",
		ScoreRef:      "test-ref",
		Files: []File{
			{Path: "bin/bcftools", SHA256: sum(bcf), Mode: 0o755},
			{Path: "plugins/liftover.so", SHA256: sum(plugin), Mode: 0o644},
		},
	}
	mb, _ := json.Marshal(manifest)
	mem := fstest.MapFS{
		"payload/linux-amd64/manifest.json":       &fstest.MapFile{Data: mb},
		"payload/linux-amd64/bin/bcftools":        &fstest.MapFile{Data: bcf},
		"payload/linux-amd64/plugins/liftover.so": &fstest.MapFile{Data: plugin},
	}
	root := t.TempDir()
	m := &Manager{Root: root, FS: mem}
	inst, gotManifest, err := m.installFor(platform)
	if err != nil {
		t.Fatal(err)
	}
	if gotManifest.ScoreRef != "test-ref" {
		t.Fatalf("score ref=%q", gotManifest.ScoreRef)
	}
	if _, err := os.Stat(inst.BCFTools); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inst.PluginDir, "liftover.so")); err != nil {
		t.Fatal(err)
	}

	// Corrupt one installed file; reinstall must restore it from the embedded bundle.
	if err := os.WriteFile(inst.BCFTools, []byte("corrupt"), 0o755); err != nil {
		t.Fatal(err)
	}
	inst2, _, err := m.installFor(platform)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(inst2.BCFTools)
	if string(b) != string(bcf) {
		t.Fatalf("bundle was not repaired: %q", b)
	}
}

func TestBundleUnavailable(t *testing.T) {
	m := &Manager{Root: t.TempDir(), FS: fstest.MapFS{}}
	_, _, err := m.installFor("windows-amd64")
	if err == nil || !isErr(err, ErrBundleUnavailable) {
		t.Fatalf("expected ErrBundleUnavailable, got %v", err)
	}
}

func isErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var _ fs.FS = fstest.MapFS{}

func TestInstallArchiveAndFindInstalled(t *testing.T) {
	platform := PlatformKey()
	bcfName := "bin/bcftools"
	if platform == "windows-amd64" || strings.HasPrefix(platform, "windows-") {
		bcfName = "bin/bcftools.exe"
	}
	bcf := []byte("fake-bcftools")
	plugin := []byte("fake-plugin")
	manifest := Manifest{
		FormatVersion: BundleFormatVersion,
		EngineVersion: "bcftools-1.24-archive-test",
		Platform:      platform,
		BCFTools:      bcfName,
		PluginDir:     "plugins",
		BCFToolsVer:   "bcftools 1.24",
		ScoreRef:      "archive-test",
		Files: []File{
			{Path: bcfName, SHA256: sum(bcf), Mode: 0o755},
			{Path: "plugins/liftover.so", SHA256: sum(plugin), Mode: 0o644},
		},
	}
	zipPath := filepath.Join(t.TempDir(), "engine.zip")
	if err := writeTestBundle(zipPath, manifest, map[string][]byte{bcfName: bcf, "plugins/liftover.so": plugin}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(t.TempDir())
	inst, got, err := m.InstallArchive(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.EngineVersion != manifest.EngineVersion {
		t.Fatalf("engine version=%q", got.EngineVersion)
	}
	if _, err := os.Stat(inst.BCFTools); err != nil {
		t.Fatal(err)
	}
	found, foundManifest, err := m.FindInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if found.BCFTools != inst.BCFTools || foundManifest.EngineVersion != manifest.EngineVersion {
		t.Fatalf("installed engine lookup mismatch: %#v %#v", found, foundManifest)
	}
}

func writeTestBundle(path string, manifest Manifest, payload map[string][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	mb, _ := json.Marshal(manifest)
	w, err := zw.Create("manifest.json")
	if err != nil {
		_ = f.Close()
		return err
	}
	if _, err := w.Write(mb); err != nil {
		_ = f.Close()
		return err
	}
	for name, data := range payload {
		w, err := zw.Create(name)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := w.Write(data); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

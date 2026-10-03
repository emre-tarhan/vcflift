package enginebundle

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/engine"
)

type currentPointer struct {
	EngineVersion string `json:"engine_version"`
	Platform      string `json:"platform"`
}

// InstallArchive installs a validated .zip engine bundle into the VCF Lift
// cache. It only extracts files declared by manifest.json and verifies every
// SHA-256 before activation.
func (m *Manager) InstallArchive(archivePath string) (engine.Installation, Manifest, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return engine.Installation{}, Manifest{}, fmt.Errorf("open engine bundle: %w", err)
	}
	defer zr.Close()

	entries := make(map[string]*zip.File, len(zr.File))
	for _, zf := range zr.File {
		clean := filepath.ToSlash(filepath.Clean(zf.Name))
		if clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			return engine.Installation{}, Manifest{}, fmt.Errorf("unsafe path in engine bundle: %q", zf.Name)
		}
		entries[clean] = zf
	}
	mf := entries["manifest.json"]
	if mf == nil {
		return engine.Installation{}, Manifest{}, fmt.Errorf("engine bundle has no manifest.json")
	}
	rc, err := mf.Open()
	if err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	mb, err := io.ReadAll(io.LimitReader(rc, 4<<20))
	closeErr := rc.Close()
	if err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	if closeErr != nil {
		return engine.Installation{}, Manifest{}, closeErr
	}
	var manifest Manifest
	if err := json.Unmarshal(mb, &manifest); err != nil {
		return engine.Installation{}, Manifest{}, fmt.Errorf("decode engine bundle manifest: %w", err)
	}
	platform := PlatformKey()
	if err := validateManifest(manifest, platform); err != nil {
		return engine.Installation{}, Manifest{}, err
	}

	dest := filepath.Join(m.Root, sanitizeSegment(manifest.EngineVersion), platform)
	stage := dest + fmt.Sprintf(".partial-%d", os.Getpid())
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stage)
		}
	}()

	for _, f := range manifest.Files {
		zf := entries[filepath.ToSlash(f.Path)]
		if zf == nil {
			return engine.Installation{}, Manifest{}, fmt.Errorf("engine bundle is missing %s", f.Path)
		}
		in, err := zf.Open()
		if err != nil {
			return engine.Installation{}, Manifest{}, err
		}
		outPath := filepath.Join(stage, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			_ = in.Close()
			return engine.Installation{}, Manifest{}, err
		}
		mode := os.FileMode(f.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if filepath.ToSlash(f.Path) == filepath.ToSlash(manifest.BCFTools) {
			mode |= 0o111
		}
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			_ = in.Close()
			return engine.Installation{}, Manifest{}, err
		}
		_, cpErr := io.Copy(out, in)
		inErr := in.Close()
		outErr := out.Close()
		if cpErr != nil {
			return engine.Installation{}, Manifest{}, cpErr
		}
		if inErr != nil {
			return engine.Installation{}, Manifest{}, inErr
		}
		if outErr != nil {
			return engine.Installation{}, Manifest{}, outErr
		}
		got, err := sha256File(outPath)
		if err != nil {
			return engine.Installation{}, Manifest{}, err
		}
		if !strings.EqualFold(got, f.SHA256) {
			return engine.Installation{}, Manifest{}, fmt.Errorf("engine bundle checksum mismatch for %s: got %s expected %s", f.Path, got, f.SHA256)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), mb, 0o644); err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	if ok, err := validateInstalled(stage, manifest); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("staged engine did not validate")
		}
		return engine.Installation{}, Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	_ = os.RemoveAll(dest)
	if err := os.Rename(stage, dest); err != nil {
		return engine.Installation{}, Manifest{}, fmt.Errorf("activate engine bundle: %w", err)
	}
	cleanup = false
	if err := m.markCurrent(manifest); err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	return installationFor(dest, manifest), manifest, nil
}

func (m *Manager) FindInstalled() (engine.Installation, Manifest, error) {
	platform := PlatformKey()
	b, err := os.ReadFile(m.currentPath(platform))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return engine.Installation{}, Manifest{}, ErrBundleUnavailable
		}
		return engine.Installation{}, Manifest{}, err
	}
	var ptr currentPointer
	if err := json.Unmarshal(b, &ptr); err != nil {
		return engine.Installation{}, Manifest{}, fmt.Errorf("decode current engine pointer: %w", err)
	}
	if ptr.Platform != platform || ptr.EngineVersion == "" {
		return engine.Installation{}, Manifest{}, fmt.Errorf("current engine pointer is invalid")
	}
	root := filepath.Join(m.Root, sanitizeSegment(ptr.EngineVersion), platform)
	mb, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return engine.Installation{}, Manifest{}, fmt.Errorf("read installed engine manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(mb, &manifest); err != nil {
		return engine.Installation{}, Manifest{}, fmt.Errorf("decode installed engine manifest: %w", err)
	}
	if err := validateManifest(manifest, platform); err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	ok, err := validateInstalled(root, manifest)
	if err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	if !ok {
		return engine.Installation{}, Manifest{}, fmt.Errorf("installed engine failed checksum validation")
	}
	return installationFor(root, manifest), manifest, nil
}

func (m *Manager) markCurrent(manifest Manifest) error {
	if m.Root == "" {
		return fmt.Errorf("engine cache directory is empty")
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return err
	}
	ptr := currentPointer{EngineVersion: manifest.EngineVersion, Platform: manifest.Platform}
	b, err := json.MarshalIndent(ptr, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	p := m.currentPath(manifest.Platform)
	partial := p + ".partial"
	if err := os.WriteFile(partial, b, 0o644); err != nil {
		return err
	}
	_ = os.Remove(p)
	return os.Rename(partial, p)
}

func (m *Manager) currentPath(platform string) string {
	return filepath.Join(m.Root, "current-"+sanitizeSegment(platform)+".json")
}

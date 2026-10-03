package enginebundle

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/cachelock"
	"github.com/emre-tarhan/vcflift/internal/engine"
)

const BundleFormatVersion = 1

var ErrBundleUnavailable = errors.New("embedded native engine is not available for this platform")

// payload is empty in the development checkout. Release workflows
// stage a platform-specific engine under payload/<goos>-<goarch>/ before building.
//
//go:embed payload
var embeddedPayload embed.FS

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode,omitempty"`
}

type Manifest struct {
	FormatVersion int    `json:"format_version"`
	EngineVersion string `json:"engine_version"`
	Platform      string `json:"platform"`
	BCFTools      string `json:"bcftools"`
	PluginDir     string `json:"plugin_dir"`
	BCFToolsVer   string `json:"bcftools_version"`
	ScoreRef      string `json:"score_ref"`
	Files         []File `json:"files"`
}

type Manager struct {
	Root string
	FS   fs.FS
}

func NewManager(root string) *Manager {
	return &Manager{Root: root, FS: embeddedPayload}
}

func PlatformKey() string { return runtime.GOOS + "-" + runtime.GOARCH }

func (m *Manager) Install() (engine.Installation, Manifest, error) {
	inst, manifest, err := m.installFor(PlatformKey())
	if err != nil {
		return inst, manifest, err
	}
	if err := m.markCurrent(manifest); err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	return inst, manifest, nil
}

func (m *Manager) installFor(platform string) (engine.Installation, Manifest, error) {
	if m.Root == "" {
		return engine.Installation{}, Manifest{}, fmt.Errorf("engine cache directory is empty")
	}
	if m.FS == nil {
		m.FS = embeddedPayload
	}

	manifestPath := path.Join("payload", platform, "manifest.json")
	manifestBytes, err := fs.ReadFile(m.FS, manifestPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return engine.Installation{}, Manifest{}, fmt.Errorf("%w: %s", ErrBundleUnavailable, platform)
		}
		return engine.Installation{}, Manifest{}, fmt.Errorf("read embedded engine manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return engine.Installation{}, Manifest{}, fmt.Errorf("decode embedded engine manifest: %w", err)
	}
	if err := validateManifest(manifest, platform); err != nil {
		return engine.Installation{}, Manifest{}, err
	}

	dest := filepath.Join(m.Root, sanitizeSegment(manifest.EngineVersion), platform)
	if ok, err := validateInstalled(dest, manifest); err == nil && ok {
		return installationFor(dest, manifest), manifest, nil
	}

	release, err := cachelock.Acquire(m.Root)
	if err != nil {
		return engine.Installation{}, Manifest{}, err
	}
	defer release()
	// Another process may have finished an install between the check above and
	// taking the lock; prefer reusing its result over restaging.
	if ok, err := validateInstalled(dest, manifest); err == nil && ok {
		return installationFor(dest, manifest), manifest, nil
	}

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
		srcPath := path.Join("payload", platform, path.Clean(f.Path))
		b, err := fs.ReadFile(m.FS, srcPath)
		if err != nil {
			return engine.Installation{}, Manifest{}, fmt.Errorf("read embedded engine file %s: %w", f.Path, err)
		}
		if got := sha256Bytes(b); !strings.EqualFold(got, f.SHA256) {
			return engine.Installation{}, Manifest{}, fmt.Errorf("embedded engine checksum mismatch for %s: got %s expected %s", f.Path, got, f.SHA256)
		}
		out := filepath.Join(stage, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return engine.Installation{}, Manifest{}, err
		}
		mode := os.FileMode(f.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if runtime.GOOS != "windows" && filepath.ToSlash(f.Path) == filepath.ToSlash(manifest.BCFTools) {
			mode |= 0o111
		}
		if err := os.WriteFile(out, b, mode); err != nil {
			return engine.Installation{}, Manifest{}, fmt.Errorf("extract engine file %s: %w", f.Path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), manifestBytes, 0o644); err != nil {
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
		return engine.Installation{}, Manifest{}, fmt.Errorf("activate embedded engine: %w", err)
	}
	cleanup = false
	return installationFor(dest, manifest), manifest, nil
}

func validateManifest(m Manifest, platform string) error {
	if m.FormatVersion != BundleFormatVersion {
		return fmt.Errorf("unsupported engine bundle manifest format %d", m.FormatVersion)
	}
	if m.EngineVersion == "" || m.Platform == "" || m.BCFTools == "" || m.PluginDir == "" {
		return fmt.Errorf("embedded engine manifest is incomplete")
	}
	if m.Platform != platform {
		return fmt.Errorf("embedded engine platform mismatch: manifest=%s runtime=%s", m.Platform, platform)
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("embedded engine manifest contains no files")
	}
	for _, f := range m.Files {
		clean := path.Clean(f.Path)
		if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || f.SHA256 == "" {
			return fmt.Errorf("invalid embedded engine file entry %q", f.Path)
		}
	}
	return nil
}

func validateInstalled(root string, manifest Manifest) (bool, error) {
	for _, f := range manifest.Files {
		p := filepath.Join(root, filepath.FromSlash(f.Path))
		got, err := sha256File(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		if !strings.EqualFold(got, f.SHA256) {
			return false, nil
		}
	}
	return true, nil
}

func installationFor(root string, manifest Manifest) engine.Installation {
	return engine.Installation{
		BCFTools:  filepath.Join(root, filepath.FromSlash(manifest.BCFTools)),
		PluginDir: filepath.Join(root, filepath.FromSlash(manifest.PluginDir)),
		Version:   manifest.BCFToolsVer,
	}
}

func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "..", "-")
	s = replacer.Replace(s)
	if s == "" {
		return "unknown"
	}
	return s
}

func sha256Bytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sha256File(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return sha256Bytes(b), nil
}

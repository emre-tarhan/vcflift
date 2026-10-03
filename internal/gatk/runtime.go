package gatk

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emre-tarhan/vcflift/internal/cachelock"
	"github.com/emre-tarhan/vcflift/internal/httpretry"

	"github.com/emre-tarhan/vcflift/internal/model"
)

const (
	DefaultGATKVersion = "4.7.0.0"
	DefaultJavaVersion = "17.0.20.1+1"
)

var ErrUnsupportedPlatform = errors.New("portable GATK runtime is not available for this platform")

type ArchiveKind string

const (
	ArchiveZip   ArchiveKind = "zip"
	ArchiveTarGZ ArchiveKind = "tar.gz"
)

type Asset struct {
	Name             string
	URL              string
	SHA256           string
	ChecksumURL      string
	GitHubReleaseAPI string
	GitHubAssetName  string
	Filename         string
	Archive          ArchiveKind
}

type Catalog struct {
	GATKVersion string
	JavaVersion string
	GATK        Asset
	Java        map[string]Asset
}

func DefaultCatalog() Catalog {
	temurinTag := "jdk-17.0.20.1%2B1"
	temurinBase := "https://github.com/adoptium/temurin17-binaries/releases/download/" + temurinTag + "/"
	return Catalog{
		GATKVersion: DefaultGATKVersion,
		JavaVersion: DefaultJavaVersion,
		GATK: Asset{
			Name:             "GATK " + DefaultGATKVersion,
			GitHubReleaseAPI: "https://api.github.com/repos/broadinstitute/gatk/releases/tags/" + DefaultGATKVersion,
			GitHubAssetName:  "gatk-" + DefaultGATKVersion + ".zip",
			Filename:         "gatk-" + DefaultGATKVersion + ".zip",
			Archive:          ArchiveZip,
		},
		Java: map[string]Asset{
			"windows-amd64": {
				Name:        "Eclipse Temurin JRE " + DefaultJavaVersion + " (Windows x64)",
				URL:         temurinBase + "OpenJDK17U-jre_x64_windows_hotspot_17.0.20.1_1.zip",
				ChecksumURL: temurinBase + "OpenJDK17U-jre_x64_windows_hotspot_17.0.20.1_1.zip.sha256.txt",
				Filename:    "OpenJDK17U-jre_x64_windows_hotspot_17.0.20.1_1.zip",
				Archive:     ArchiveZip,
			},
			"linux-amd64": {
				Name:        "Eclipse Temurin JRE " + DefaultJavaVersion + " (Linux x64)",
				URL:         temurinBase + "OpenJDK17U-jre_x64_linux_hotspot_17.0.20.1_1.tar.gz",
				ChecksumURL: temurinBase + "OpenJDK17U-jre_x64_linux_hotspot_17.0.20.1_1.tar.gz.sha256.txt",
				Filename:    "OpenJDK17U-jre_x64_linux_hotspot_17.0.20.1_1.tar.gz",
				Archive:     ArchiveTarGZ,
			},
		},
	}
}

type Manager struct {
	Root       string
	HTTPClient *http.Client
	Catalog    Catalog
}

func NewManager(root string) *Manager {
	return &Manager{
		Root:       root,
		HTTPClient: &http.Client{Timeout: 0},
		Catalog:    DefaultCatalog(),
	}
}

func PlatformKey() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Prepare installs a pinned Java 17 runtime and GATK package into the local cache.
// Nothing is downloaded unless the caller explicitly asks for gVCF support.
// Java and GATK are independent artifacts, so first-use downloads run in parallel.
func (m *Manager) Prepare(ctx context.Context, progress func(model.ProgressEvent)) (Installation, error) {
	if m.Root == "" {
		return Installation{}, fmt.Errorf("GATK runtime cache directory is empty")
	}
	if m.HTTPClient == nil {
		m.HTTPClient = &http.Client{}
	}
	cat := m.Catalog
	if cat.GATKVersion == "" {
		cat = DefaultCatalog()
	}
	javaAsset, ok := cat.Java[PlatformKey()]
	if !ok {
		return Installation{}, fmt.Errorf("%w: %s", ErrUnsupportedPlatform, PlatformKey())
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return Installation{}, err
	}
	release, err := cachelock.Acquire(m.Root)
	if err != nil {
		return Installation{}, err
	}
	defer release()

	var progressMu sync.Mutex
	emit := progress
	if progress != nil {
		emit = func(e model.ProgressEvent) {
			progressMu.Lock()
			defer progressMu.Unlock()
			progress(e)
		}
	}

	javaRoot := filepath.Join(m.Root, "java", sanitizeVersion(cat.JavaVersion), PlatformKey())
	javaExe := findJavaExecutable(javaRoot)
	gatkRoot := filepath.Join(m.Root, "gatk", sanitizeVersion(cat.GATKVersion))
	gatkJar := filepath.Join(gatkRoot, "gatk-package.jar")

	type archiveResult struct {
		kind string
		path string
		err  error
	}
	needed := 0
	results := make(chan archiveResult, 2)
	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if javaExe == "" {
		needed++
		go func() {
			if emit != nil {
				emit(model.ProgressEvent{Stage: model.StageRuntime, Item: "java", Message: "Preparing Java 17 runtime for gVCF genotyping"})
			}
			archive, err := m.ensureArchive(downloadCtx, javaAsset, emit)
			if err != nil {
				cancel()
			}
			results <- archiveResult{kind: "java", path: archive, err: err}
		}()
	}
	if !regularFile(gatkJar) {
		needed++
		go func() {
			if emit != nil {
				emit(model.ProgressEvent{Stage: model.StageRuntime, Item: "gatk", Message: "Preparing GATK " + cat.GATKVersion})
			}
			archive, err := m.ensureArchive(downloadCtx, cat.GATK, emit)
			if err != nil {
				cancel()
			}
			results <- archiveResult{kind: "gatk", path: archive, err: err}
		}()
	}

	archives := map[string]string{}
	for i := 0; i < needed; i++ {
		res := <-results
		if res.err != nil {
			return Installation{}, fmt.Errorf("prepare %s runtime: %w", res.kind, res.err)
		}
		archives[res.kind] = res.path
	}

	if javaExe == "" {
		archive := archives["java"]
		if emit != nil {
			emit(model.ProgressEvent{Stage: model.StageRuntime, Item: "java", Message: "Installing Java 17 runtime"})
		}
		if err := extractArchiveAtomic(archive, javaAsset.Archive, javaRoot); err != nil {
			return Installation{}, fmt.Errorf("extract Java runtime: %w", err)
		}
		javaExe = findJavaExecutable(javaRoot)
		if javaExe == "" {
			return Installation{}, fmt.Errorf("Java executable not found after extracting %s", javaAsset.Name)
		}
		_ = os.Remove(archive)
	}

	if !regularFile(gatkJar) {
		archive := archives["gatk"]
		if emit != nil {
			emit(model.ProgressEvent{Stage: model.StageRuntime, Item: "gatk", Message: "Installing GATK " + cat.GATKVersion})
		}
		if err := extractGATKJarAtomic(archive, gatkRoot, cat.GATKVersion); err != nil {
			return Installation{}, fmt.Errorf("extract GATK package: %w", err)
		}
		_ = os.Remove(archive)
	}

	return Installation{Java: javaExe, Jar: gatkJar, Source: "managed"}, nil
}

func (m *Manager) ensureArchive(ctx context.Context, asset Asset, progress func(model.ProgressEvent)) (string, error) {
	if asset.Filename == "" {
		return "", fmt.Errorf("asset filename is empty")
	}
	downloads := filepath.Join(m.Root, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		return "", err
	}
	resolved, expected, err := m.resolveAsset(ctx, asset)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(downloads, asset.Filename)
	if regularFile(dst) {
		got, err := sha256File(dst)
		if err == nil && strings.EqualFold(got, expected) {
			return dst, nil
		}
		_ = os.Remove(dst)
	}
	if progress != nil {
		progress(model.ProgressEvent{Stage: model.StageRuntime, Item: asset.Filename, Message: "Downloading " + asset.Name})
	}
	if err := m.download(ctx, resolved, dst, func(cur, total int64) {
		if progress != nil {
			progress(model.ProgressEvent{Stage: model.StageRuntime, Item: asset.Filename, Message: "Downloading " + asset.Name, Current: cur, Total: total})
		}
	}); err != nil {
		return "", err
	}
	got, err := sha256File(dst)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, expected) {
		_ = os.Remove(dst)
		return "", fmt.Errorf("SHA-256 mismatch for %s: got %s expected %s", asset.Name, got, expected)
	}
	return dst, nil
}

func (m *Manager) resolveAsset(ctx context.Context, asset Asset) (string, string, error) {
	if asset.GitHubReleaseAPI != "" {
		return m.resolveGitHubAsset(ctx, asset)
	}
	if asset.URL == "" {
		return "", "", fmt.Errorf("download URL missing for %s", asset.Name)
	}
	sum := normalizeSHA256(asset.SHA256)
	if sum == "" && asset.ChecksumURL != "" {
		var err error
		sum, err = m.fetchChecksum(ctx, asset.ChecksumURL)
		if err != nil {
			return "", "", err
		}
	}
	if sum == "" {
		return "", "", fmt.Errorf("no trusted SHA-256 configured for %s", asset.Name)
	}
	return asset.URL, sum, nil
}

type githubRelease struct {
	Assets []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Digest             string `json:"digest"`
	} `json:"assets"`
}

func (m *Manager) resolveGitHubAsset(ctx context.Context, asset Asset) (string, string, error) {
	resp, err := httpretry.Do(ctx, m.HTTPClient, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.GitHubReleaseAPI, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "VCFLift/"+DefaultGATKVersion)
		return req, nil
	})
	if err != nil {
		return "", "", fmt.Errorf("GitHub release metadata: %w", err)
	}
	defer resp.Body.Close()
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&release); err != nil {
		return "", "", err
	}
	for _, a := range release.Assets {
		if a.Name != asset.GitHubAssetName {
			continue
		}
		digest := strings.TrimSpace(a.Digest)
		digest = strings.TrimPrefix(strings.ToLower(digest), "sha256:")
		digest = normalizeSHA256(digest)
		if digest == "" {
			return "", "", fmt.Errorf("GitHub did not provide a SHA-256 digest for pinned asset %s", a.Name)
		}
		if a.BrowserDownloadURL == "" {
			return "", "", fmt.Errorf("GitHub asset %s has no download URL", a.Name)
		}
		return a.BrowserDownloadURL, digest, nil
	}
	return "", "", fmt.Errorf("GitHub release asset %q not found", asset.GitHubAssetName)
}

func (m *Manager) fetchChecksum(ctx context.Context, url string) (string, error) {
	resp, err := httpretry.Do(ctx, m.HTTPClient, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "VCFLift/"+DefaultGATKVersion)
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("checksum: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum response")
	}
	sum := normalizeSHA256(fields[0])
	if sum == "" {
		return "", fmt.Errorf("invalid SHA-256 checksum response")
	}
	return sum, nil
}

func (m *Manager) download(ctx context.Context, url, dst string, progress func(int64, int64)) error {
	// Resumable .part transfers make retries cheap; only transient failures
	// (network errors, 408/429/5xx) are retried, everything else fails fast.
	var lastErr error
	for attempt := 0; attempt < httpretry.Attempts; attempt++ {
		err := m.downloadOnce(ctx, url, dst, progress)
		if err == nil {
			return nil
		}
		if !httpretry.Retryable(err) {
			return err
		}
		lastErr = err
		if attempt < httpretry.Attempts-1 {
			if serr := httpretry.Sleep(ctx, attempt); serr != nil {
				return serr
			}
		}
	}
	return lastErr
}

func (m *Manager) downloadOnce(ctx context.Context, url, dst string, progress func(int64, int64)) error {
	partial := dst + ".part"
	var start int64
	if st, err := os.Stat(partial); err == nil {
		start = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "VCFLift/"+DefaultGATKVersion)
	if start > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-")
	}
	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &httpretry.StatusError{Code: resp.StatusCode}
	}
	appendMode := start > 0 && resp.StatusCode == http.StatusPartialContent
	if start > 0 && !appendMode {
		start = 0
	}
	flags := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(partial, flags, 0o644)
	if err != nil {
		return err
	}
	total := resp.ContentLength
	if total >= 0 {
		total += start
	}
	buf := make([]byte, 1<<20)
	cur := start
	last := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				_ = f.Close()
				return err
			}
			cur += int64(n)
			if progress != nil && (time.Since(last) >= 150*time.Millisecond || (total >= 0 && cur == total)) {
				progress(cur, total)
				last = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			return readErr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(partial, dst)
}

func extractArchiveAtomic(archive string, kind ArchiveKind, finalRoot string) error {
	if findJavaExecutable(finalRoot) != "" {
		return nil
	}
	stage := finalRoot + ".partial"
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	var err error
	switch kind {
	case ArchiveZip:
		err = extractZipAll(archive, stage)
	case ArchiveTarGZ:
		err = extractTarGZAll(archive, stage)
	default:
		err = fmt.Errorf("unsupported archive kind %q", kind)
	}
	if err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	if findJavaExecutable(stage) == "" {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("archive contains no Java executable")
	}
	_ = os.RemoveAll(finalRoot)
	return os.Rename(stage, finalRoot)
}

func extractGATKJarAtomic(archive, finalRoot, version string) error {
	if regularFile(filepath.Join(finalRoot, "gatk-package.jar")) {
		return nil
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	targetName := "gatk-package-" + version + "-local.jar"
	var entry *zip.File
	for _, f := range zr.File {
		if filepath.Base(f.Name) == targetName {
			if entry != nil {
				return fmt.Errorf("multiple %s entries in GATK archive", targetName)
			}
			entry = f
		}
	}
	if entry == nil {
		return fmt.Errorf("%s not found in GATK archive", targetName)
	}
	stage := finalRoot + ".partial"
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	src, err := entry.Open()
	if err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	dstPath := filepath.Join(stage, "gatk-package.jar")
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		_ = src.Close()
		_ = os.RemoveAll(stage)
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	_ = src.Close()
	if copyErr != nil {
		_ = os.RemoveAll(stage)
		return copyErr
	}
	if closeErr != nil {
		_ = os.RemoveAll(stage)
		return closeErr
	}
	_ = os.RemoveAll(finalRoot)
	return os.Rename(stage, finalRoot)
}

func extractZipAll(path, root string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		dst, err := safeArchivePath(root, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			_ = src.Close()
			return err
		}
		_, copyErr := io.Copy(out, src)
		closeErr := out.Close()
		_ = src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractTarGZAll(path, root string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		dst, err := safeArchivePath(root, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(h.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(dst), filepath.FromSlash(h.Linkname)))
			if !withinRoot(root, resolved) {
				return fmt.Errorf("unsafe archive symlink %q -> %q", h.Name, h.Linkname)
			}
			_ = os.Remove(dst)
			if err := os.Symlink(h.Linkname, dst); err != nil {
				return err
			}
		case tar.TypeLink:
			linkTarget, err := safeArchivePath(root, h.Linkname)
			if err != nil {
				return fmt.Errorf("unsafe archive hardlink %q -> %q: %w", h.Name, h.Linkname, err)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			_ = os.Remove(dst)
			if err := os.Link(linkTarget, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func safeArchivePath(root, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == "" {
		return root, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	dst := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, dst)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return dst, nil
}

func findJavaExecutable(root string) string {
	if root == "" {
		return ""
	}
	want := JavaExecutableName()
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() || !strings.EqualFold(d.Name(), want) {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "bin" {
			return nil
		}
		found = path
		return nil
	})
	return found
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
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

func normalizeSHA256(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(s); err != nil {
		return ""
	}
	return s
}

func regularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func sanitizeVersion(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "+", "_")
	return r.Replace(s)
}

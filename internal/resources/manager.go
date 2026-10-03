package resources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emre-tarhan/vcflift/internal/cachelock"
	"github.com/emre-tarhan/vcflift/internal/diskspace"
	"github.com/emre-tarhan/vcflift/internal/httpretry"
	"github.com/emre-tarhan/vcflift/internal/model"
)

var ErrLicenseAcceptanceRequired = errors.New("resource license acceptance required")

type Manager struct {
	Root                 string
	HTTPClient           *http.Client
	AcceptRestrictedData bool
	MaxParallel          int
	KeepArchives         bool
	SkipDiskPreflight    bool
}

type Prepared struct {
	Root        string
	HG38FASTA   string
	HG19FASTA   string
	Chain       string
	HG38Aliases string
}

func NewManager(root string) *Manager {
	return &Manager{
		Root:        root,
		HTTPClient:  &http.Client{Timeout: 0},
		MaxParallel: 3,
	}
}

func (m *Manager) Prepare(ctx context.Context, manifest Manifest, progress func(model.ProgressEvent)) (Prepared, error) {
	if m.Root == "" {
		return Prepared{}, fmt.Errorf("resource cache directory is empty")
	}
	if m.HTTPClient == nil {
		m.HTTPClient = &http.Client{}
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return Prepared{}, err
	}
	release, err := cachelock.Acquire(m.Root)
	if err != nil {
		return Prepared{}, err
	}
	defer release()

	for _, r := range manifest.Resources {
		if r.RequiresAcceptance && !m.AcceptRestrictedData && !m.resourceReady(r) {
			return Prepared{}, fmt.Errorf("%w: %s (%s)", ErrLicenseAcceptanceRequired, r.Name, r.LicenseURL)
		}
	}

	if !m.SkipDiskPreflight {
		if need := m.estimatedPeakBytes(manifest); need > 0 {
			if available, err := diskspace.Available(m.Root); err == nil && available < uint64(need) {
				return Prepared{}, fmt.Errorf("insufficient free space for reference preparation: need about %.1f GiB, available %.1f GiB in %s",
					gib(uint64(need)), gib(available), m.Root)
			}
		}
	}

	var progressMu sync.Mutex
	emit := progress
	if progress != nil {
		emit = func(e model.ProgressEvent) {
			progressMu.Lock()
			defer progressMu.Unlock()
			progress(e)
		}
	}

	parallel := m.MaxParallel
	if parallel <= 0 {
		parallel = 3
	}
	if parallel > len(manifest.Resources) {
		parallel = len(manifest.Resources)
	}
	if parallel < 1 {
		parallel = 1
	}

	type result struct {
		id   string
		path string
		err  error
	}
	jobs := make(chan Resource)
	results := make(chan result, len(manifest.Resources))
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range jobs {
				p, err := m.ensureResource(workerCtx, r, emit)
				if err != nil {
					cancel()
				}
				results <- result{id: r.ID, path: p, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, r := range manifest.Resources {
			select {
			case jobs <- r:
			case <-workerCtx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	paths := map[string]string{}
	var firstErr error
	for res := range results {
		if res.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("prepare %s: %w", res.id, res.err)
			continue
		}
		if res.err == nil {
			paths[res.id] = res.path
		}
	}
	if firstErr != nil {
		return Prepared{}, firstErr
	}

	return Prepared{
		Root:        m.Root,
		HG38FASTA:   paths["hg38_fasta"],
		HG19FASTA:   paths["hg19_fasta"],
		Chain:       paths["hg38_to_hg19_chain"],
		HG38Aliases: paths["hg38_aliases"],
	}, nil
}

func (m *Manager) ensureResource(ctx context.Context, r Resource, progress func(model.ProgressEvent)) (string, error) {
	final := filepath.Join(m.Root, r.Filename)
	prepared := final
	if r.Transform == TransformGzipFASTA {
		if r.PreparedFilename == "" {
			return "", fmt.Errorf("prepared filename missing")
		}
		prepared = filepath.Join(m.Root, r.PreparedFilename)
		if regularFile(prepared) && regularFile(prepared+".fai") && regularFile(referenceDictPath(prepared)) {
			if !m.KeepArchives {
				_ = os.Remove(final)
				_ = os.Remove(final + ".part")
			}
			return prepared, nil
		}
	}

	expected, err := m.expectedMD5(ctx, r, final)
	if err != nil {
		return "", err
	}
	if regularFile(final) {
		got, err := md5File(final)
		if err == nil && strings.EqualFold(got, expected) {
			out, err := m.transformIfNeeded(r, final, prepared, progress)
			if err == nil && r.Transform == TransformGzipFASTA && !m.KeepArchives {
				_ = os.Remove(final)
			}
			return out, err
		}
		_ = os.Remove(final)
	}

	if progress != nil {
		progress(model.ProgressEvent{Stage: model.StageResources, Item: r.ID, Message: "Downloading " + r.Name})
	}
	if err := m.download(ctx, r.URL, final, func(cur, total int64) {
		if progress != nil {
			progress(model.ProgressEvent{Stage: model.StageResources, Item: r.ID, Message: "Downloading " + r.Name, Current: cur, Total: total})
		}
	}); err != nil {
		return "", err
	}
	got, err := md5File(final)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, expected) {
		_ = os.Remove(final)
		return "", fmt.Errorf("checksum mismatch: got %s, expected %s", got, expected)
	}
	out, err := m.transformIfNeeded(r, final, prepared, progress)
	if err == nil && r.Transform == TransformGzipFASTA && !m.KeepArchives {
		_ = os.Remove(final)
	}
	return out, err
}

func (m *Manager) transformIfNeeded(r Resource, downloaded, prepared string, progress func(model.ProgressEvent)) (string, error) {
	switch r.Transform {
	case TransformNone, "":
		return downloaded, nil
	case TransformGzipFASTA:
		if regularFile(prepared) && regularFile(prepared+".fai") && regularFile(referenceDictPath(prepared)) {
			return prepared, nil
		}
		if progress != nil {
			progress(model.ProgressEvent{Stage: model.StageResources, Item: r.ID, Message: "Preparing " + r.Name})
		}
		if err := prepareGzipFASTA(downloaded, prepared); err != nil {
			return "", err
		}
		return prepared, nil
	default:
		return "", fmt.Errorf("unsupported resource transform %q", r.Transform)
	}
}

func (m *Manager) expectedMD5(ctx context.Context, r Resource, finalPath string) (string, error) {
	if r.MD5 != "" {
		return strings.ToLower(r.MD5), nil
	}
	if b, err := os.ReadFile(finalPath + ".md5"); err == nil {
		if sum := strings.TrimSpace(string(b)); len(sum) == md5HexLength {
			return strings.ToLower(sum), nil
		}
	}
	if r.ChecksumIndexURL == "" || r.ChecksumIndexName == "" {
		return "", fmt.Errorf("no checksum configured")
	}
	resp, err := httpretry.Do(ctx, m.HTTPClient, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, r.ChecksumIndexURL, nil)
	})
	if err != nil {
		return "", fmt.Errorf("checksum index: %w", err)
	}
	defer resp.Body.Close()
	sum, err := parseChecksumIndex(resp.Body, r.ChecksumIndexName)
	if err != nil {
		return "", err
	}
	_ = os.WriteFile(finalPath+".md5", []byte(sum+"\n"), 0o644)
	return sum, nil
}

const md5HexLength = 32

func (m *Manager) resourceReady(r Resource) bool {
	final := filepath.Join(m.Root, r.Filename)
	if r.Transform == TransformGzipFASTA && r.PreparedFilename != "" {
		prepared := filepath.Join(m.Root, r.PreparedFilename)
		return regularFile(prepared) && regularFile(prepared+".fai") && regularFile(referenceDictPath(prepared))
	}
	return regularFile(final)
}

func (m *Manager) estimatedPeakBytes(manifest Manifest) int64 {
	var total int64
	for _, r := range manifest.Resources {
		if m.resourceReady(r) {
			continue
		}
		total += r.DownloadBytes + r.PreparedBytes
	}
	if total == 0 {
		return 0
	}
	// Allow room for indexes, dictionaries, temporary files and filesystem
	// overhead while two FASTAs are prepared concurrently.
	return total + (1 << 30)
}

func gib(v uint64) float64 { return float64(v) / float64(uint64(1)<<30) }

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
	if start > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-")
	}
	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	appendMode := start > 0 && resp.StatusCode == http.StatusPartialContent
	if resp.StatusCode/100 != 2 {
		return &httpretry.StatusError{Code: resp.StatusCode}
	}
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
			if progress != nil && (time.Since(last) > 150*time.Millisecond || (total >= 0 && cur == total)) {
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

func regularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

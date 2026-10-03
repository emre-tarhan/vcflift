package resources

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestManagerDownloadsAndVerifies(t *testing.T) {
	payload := []byte("chain-data")
	h := md5.Sum(payload)
	sum := hex.EncodeToString(h[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	mux.HandleFunc("/md5", func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprintf(w, "%s  test.chain.gz\n", sum) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m := NewManager(t.TempDir())
	manifest := Manifest{Version: 1, Resources: []Resource{{
		ID: "hg38_to_hg19_chain", Name: "test", URL: srv.URL + "/file", Filename: "test.chain.gz",
		ChecksumIndexURL: srv.URL + "/md5", ChecksumIndexName: "test.chain.gz",
	}}}
	_, err := m.ensureResource(context.Background(), manifest.Resources[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "test.chain.gz")); err != nil {
		t.Fatal(err)
	}
}

func TestRestrictedResourceRequiresAcceptance(t *testing.T) {
	m := NewManager(t.TempDir())
	manifest := Manifest{Resources: []Resource{{ID: "hg38_to_hg19_chain", Name: "restricted", RequiresAcceptance: true, LicenseURL: "https://example.invalid"}}}
	_, err := m.Prepare(context.Background(), manifest, nil)
	if err == nil {
		t.Fatal("expected license acceptance error")
	}
}

func TestDownloadResumesPartialFile(t *testing.T) {
	payload := []byte("0123456789abcdef")
	var sawRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRange = r.Header.Get("Range")
		if sawRange == "bytes=5-" {
			w.Header().Set("Content-Range", "bytes 5-15/16")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[5:])
			return
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	d := t.TempDir()
	dst := filepath.Join(d, "resource.bin")
	if err := os.WriteFile(dst+".part", payload[:5], 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(d)
	if err := m.download(context.Background(), srv.URL, dst, nil); err != nil {
		t.Fatal(err)
	}
	if sawRange != "bytes=5-" {
		t.Fatalf("range=%q", sawRange)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q", got)
	}
}

func TestPrepareDownloadsIndependentResourcesInParallel(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	payload := []byte("parallel-data")
	h := md5.Sum(payload)
	sum := hex.EncodeToString(h[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write(payload)
		mu.Lock()
		active--
		mu.Unlock()
	}))
	defer srv.Close()

	manifest := Manifest{Resources: []Resource{
		{ID: "a", Name: "a", URL: srv.URL + "/a", Filename: "a", MD5: sum},
		{ID: "b", Name: "b", URL: srv.URL + "/b", Filename: "b", MD5: sum},
		{ID: "c", Name: "c", URL: srv.URL + "/c", Filename: "c", MD5: sum},
	}}
	m := NewManager(t.TempDir())
	m.MaxParallel = 3
	if _, err := m.Prepare(context.Background(), manifest, nil); err != nil {
		t.Fatal(err)
	}
	if maxActive < 2 {
		t.Fatalf("downloads did not overlap, max active=%d", maxActive)
	}
}

func TestPreparedFASTADeletesDownloadArchiveByDefault(t *testing.T) {
	d := t.TempDir()
	m := NewManager(d)
	r := Resource{ID: "ref", Name: "ref", Filename: "ref.fa.gz", PreparedFilename: "ref.fa", Transform: TransformGzipFASTA}
	for _, p := range []string{"ref.fa", "ref.fa.fai", "ref.dict", "ref.fa.gz"} {
		if err := os.WriteFile(filepath.Join(d, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.ensureResource(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(d, "ref.fa") {
		t.Fatalf("prepared path=%q", got)
	}
	if _, err := os.Stat(filepath.Join(d, "ref.fa.gz")); !os.IsNotExist(err) {
		t.Fatalf("archive should be removed, err=%v", err)
	}
}

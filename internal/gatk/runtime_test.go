package gatk

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestManagerPrepareFromPinnedArchives(t *testing.T) {
	d := t.TempDir()
	javaZip := filepath.Join(d, "java.zip")
	makeZip(t, javaZip, map[string]zipEntry{
		"jdk-test/bin/" + JavaExecutableName(): {Body: fakeJavaScript(), Mode: 0o755},
	})
	gatkZip := filepath.Join(d, "gatk.zip")
	makeZip(t, gatkZip, map[string]zipEntry{
		"gatk-test/gatk-package-test-local.jar": {Body: "jar fixture", Mode: 0o644},
	})
	javaBytes, _ := os.ReadFile(javaZip)
	gatkBytes, _ := os.ReadFile(gatkZip)
	javaSum := sha256.Sum256(javaBytes)
	gatkSum := sha256.Sum256(gatkBytes)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/java.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(javaBytes) })
	mux.HandleFunc("/java.sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  java.zip\n", hex.EncodeToString(javaSum[:]))
	})
	mux.HandleFunc("/gatk.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(gatkBytes) })
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{{
			"name": "gatk.zip", "browser_download_url": srv.URL + "/gatk.zip", "digest": "sha256:" + hex.EncodeToString(gatkSum[:]),
		}}})
	})

	key := PlatformKey()
	m := NewManager(filepath.Join(d, "cache"))
	m.Catalog = Catalog{
		GATKVersion: "test",
		JavaVersion: "17-test",
		GATK:        Asset{Name: "GATK test", GitHubReleaseAPI: srv.URL + "/release", GitHubAssetName: "gatk.zip", Filename: "gatk.zip", Archive: ArchiveZip},
		Java:        map[string]Asset{key: {Name: "Java test", URL: srv.URL + "/java.zip", ChecksumURL: srv.URL + "/java.sha256", Filename: "java.zip", Archive: ArchiveZip}},
	}
	inst, err := m.Prepare(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regularFile(inst.Java) || !regularFile(inst.Jar) {
		t.Fatalf("missing prepared runtime: %+v", inst)
	}
	if inst.Source != "managed" {
		t.Fatalf("source=%q", inst.Source)
	}
	if got, _ := os.ReadFile(inst.Jar); string(got) != "jar fixture" {
		t.Fatalf("unexpected GATK jar fixture")
	}

	// A second call must use the prepared runtime without requiring archives.
	mux.HandleFunc("/should-not-be-used", func(w http.ResponseWriter, r *http.Request) { t.Fatal("unexpected redownload") })
	if _, err := m.Prepare(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSafeArchivePathRejectsTraversal(t *testing.T) {
	if _, err := safeArchivePath(t.TempDir(), "../../escape"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestNormalizeSHA256(t *testing.T) {
	s := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got := normalizeSHA256("sha256:" + s); got != s {
		t.Fatalf("got %q", got)
	}
	if normalizeSHA256("xyz") != "" {
		t.Fatal("invalid digest accepted")
	}
}

func TestJavaMajor(t *testing.T) {
	cases := map[string]string{
		`openjdk version "17.0.20.1" 2026-08-19`: "17",
		`java version "21.0.1"`:                  "21",
		`garbage`:                                "",
	}
	for in, want := range cases {
		if got := javaMajor(in); got != want {
			t.Fatalf("javaMajor(%q)=%q want %q", in, got, want)
		}
	}
}

type zipEntry struct {
	Body string
	Mode os.FileMode
}

func makeZip(t *testing.T, path string, entries map[string]zipEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, e := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(e.Mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.Body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func fakeJavaScript() string {
	if runtime.GOOS == "windows" {
		return "MZ-fake-java"
	}
	return "#!/bin/sh\necho 'openjdk version \"17.0.20.1\"' >&2\n"
}

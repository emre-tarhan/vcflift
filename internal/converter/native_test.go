package converter

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/md5"
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

	"github.com/emre-tarhan/vcflift/internal/engine"
	"github.com/emre-tarhan/vcflift/internal/gatk"
	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/resources"
)

func TestNativeConverterEndToEndWithFakeBCFTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake bcftools fixture is a POSIX shell script")
	}
	d := t.TempDir()
	fake := filepath.Join(d, "bcftools")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "bcftools 1.24"; exit 0; fi
if [ "$1" = "plugin" ] && [ "$2" = "-l" ]; then echo "liftover"; exit 0; fi
if [ "$1" = "+liftover" ] && [ "$2" = "-h" ]; then echo "--write-src --write-reject --lift-end"; exit 0; fi
cmd="$1"
shift
case "$cmd" in
  index)
    last=""
    for a in "$@"; do last="$a"; done
    : > "$last.tbi"
    exit 0
    ;;
  sort)
    out=""
    prev=""
    for a in "$@"; do
      if [ "$prev" = "-o" ]; then out="$a"; fi
      prev="$a"
    done
    gzip -c > "$out"
    exit 0
    ;;
  +liftover)
    reject=""
    prev=""
    for a in "$@"; do
      if [ "$prev" = "--reject" ]; then reject="$a"; fi
      prev="$a"
    done
    if [ -n "$reject" ]; then printf '##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n' | gzip -c > "$reject"; fi
    cat
    exit 0
    ;;
  annotate|view|norm)
    infile=""
    for a in "$@"; do
      case "$a" in
        *.vcf) if [ -f "$a" ]; then infile="$a"; fi ;;
      esac
    done
    if [ -n "$infile" ]; then cat "$infile"; else cat; fi
    exit 0
    ;;
esac
echo "unsupported fake bcftools command: $cmd" >&2
exit 9
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	fastaGZ := gzipBytes(t, ">chr1\nACGTACGT\n")
	chain := []byte("chain fixture\n")
	aliases := []byte("chr1\t1\tNC_000001.11\n")
	files := map[string][]byte{"/hg38.fa.gz": fastaGZ, "/hg19.fa.gz": fastaGZ, "/chain.gz": chain, "/aliases.txt": aliases}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	res := func(id, name string, payload []byte, transform resources.Transform, prepared string) resources.Resource {
		h := md5.Sum(payload)
		return resources.Resource{ID: id, Name: id, URL: srv.URL + name, Filename: filepath.Base(name), MD5: hex.EncodeToString(h[:]), Transform: transform, PreparedFilename: prepared}
	}
	manifest := resources.Manifest{Version: 1, Resources: []resources.Resource{
		res("hg38_fasta", "/hg38.fa.gz", fastaGZ, resources.TransformGzipFASTA, "hg38.fa"),
		res("hg19_fasta", "/hg19.fa.gz", fastaGZ, resources.TransformGzipFASTA, "hg19.fa"),
		res("hg38_to_hg19_chain", "/chain.gz", chain, resources.TransformNone, ""),
		res("hg38_aliases", "/aliases.txt", aliases, resources.TransformNone, ""),
	}}

	input := filepath.Join(d, "sample.vcf")
	vcfText := "##fileformat=VCFv4.2\n##contig=<ID=chr1,length=248956422>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nchr1\t2\t.\tC\tT\t.\tPASS\t.\n"
	if err := os.WriteFile(input, []byte(vcfText), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(d, "sample.hg19.vcf.gz")

	c := NewNative(filepath.Join(d, "cache"))
	c.Manifest = manifest
	c.Installation = engine.Installation{BCFTools: fake}
	result, err := c.Convert(context.Background(), model.JobConfig{InputPath: input, OutputPath: output}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.LiftedVariants != 1 {
		t.Fatalf("lifted=%d", result.LiftedVariants)
	}
	for _, p := range []string{output, output + ".tbi", output + ".report.json"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
}

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var path = filepath.Join(t.TempDir(), "x.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	_, _ = gz.Write([]byte(s))
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNativeConverterGVCFAutoInstallsManagedRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake bcftools/java fixtures are POSIX shell scripts")
	}
	d := t.TempDir()
	fake := filepath.Join(d, "bcftools")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "bcftools 1.24"; exit 0; fi
if [ "$1" = "plugin" ] && [ "$2" = "-l" ]; then echo "liftover"; exit 0; fi
if [ "$1" = "+liftover" ] && [ "$2" = "-h" ]; then echo "--write-src --write-reject --lift-end"; exit 0; fi
cmd="$1"
shift
case "$cmd" in
  index)
    last=""
    for a in "$@"; do last="$a"; done
    : > "$last.tbi"
    exit 0
    ;;
  sort)
    out=""
    prev=""
    for a in "$@"; do
      if [ "$prev" = "-o" ]; then out="$a"; fi
      prev="$a"
    done
    gzip -c > "$out"
    exit 0
    ;;
  +liftover)
    reject=""
    prev=""
    for a in "$@"; do
      if [ "$prev" = "--reject" ]; then reject="$a"; fi
      prev="$a"
    done
    if [ -n "$reject" ]; then printf '##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n' | gzip -c > "$reject"; fi
    cat
    exit 0
    ;;
  annotate|view|norm)
    out=""
    infile=""
    prev=""
    for a in "$@"; do
      if [ "$prev" = "-o" ]; then out="$a"; fi
      case "$a" in
        *.vcf|*.vcf.gz) if [ -f "$a" ]; then infile="$a"; fi ;;
      esac
      prev="$a"
    done
    if [ -n "$out" ]; then
      if [ -n "$infile" ]; then
        case "$infile" in *.gz) gzip -dc "$infile" | gzip -c > "$out" ;; *) gzip -c "$infile" > "$out" ;; esac
      else
        gzip -c > "$out"
      fi
    else
      if [ -n "$infile" ]; then case "$infile" in *.gz) gzip -dc "$infile" ;; *) cat "$infile" ;; esac; else cat; fi
    fi
    exit 0
    ;;
esac
echo "unsupported fake bcftools command: $cmd" >&2
exit 9
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	javaZip := filepath.Join(d, "java.zip")
	writeTestZip(t, javaZip, map[string]testZipEntry{
		"jdk/bin/java": {Body: `#!/bin/sh
if [ "$1" = "-version" ]; then echo 'openjdk version "17.0.20.1"' >&2; exit 0; fi
if [ "$1" = "-jar" ] && [ "$3" = "--version" ]; then echo 'The Genome Analysis Toolkit (GATK) vtest'; exit 0; fi
out=""
in=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-O" ]; then out="$a"; fi
  if [ "$prev" = "-V" ]; then in="$a"; fi
  prev="$a"
done
if [ -z "$out" ] || [ -z "$in" ]; then echo 'bad fake java args' >&2; exit 7; fi
gzip -dc "$in" | gzip -c > "$out"
`, Mode: 0o755},
	})
	gatkZip := filepath.Join(d, "gatk.zip")
	writeTestZip(t, gatkZip, map[string]testZipEntry{"gatk/gatk-package-test-local.jar": {Body: "jar", Mode: 0o644}})
	javaBytes, _ := os.ReadFile(javaZip)
	gatkBytes, _ := os.ReadFile(gatkZip)
	javaSHA := sha256.Sum256(javaBytes)
	gatkSHA := sha256.Sum256(gatkBytes)

	fastaGZ := gzipBytes(t, ">chr1\nACGTACGT\n")
	chain := []byte("chain fixture\n")
	aliases := []byte("chr1\t1\tNC_000001.11\n")
	files := map[string][]byte{
		"/hg38.fa.gz": fastaGZ, "/hg19.fa.gz": fastaGZ, "/chain.gz": chain, "/aliases.txt": aliases,
		"/java.zip": javaBytes, "/gatk.zip": gatkBytes,
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/java.sha256" {
			fmt.Fprintf(w, "%s  java.zip\n", hex.EncodeToString(javaSHA[:]))
			return
		}
		if r.URL.Path == "/gatk-release" {
			_ = json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{{"name": "gatk.zip", "browser_download_url": srv.URL + "/gatk.zip", "digest": "sha256:" + hex.EncodeToString(gatkSHA[:])}}})
			return
		}
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})

	res := func(id, name string, payload []byte, transform resources.Transform, prepared string) resources.Resource {
		h := md5.Sum(payload)
		return resources.Resource{ID: id, Name: id, URL: srv.URL + name, Filename: filepath.Base(name), MD5: hex.EncodeToString(h[:]), Transform: transform, PreparedFilename: prepared}
	}
	manifest := resources.Manifest{Version: 1, Resources: []resources.Resource{
		res("hg38_fasta", "/hg38.fa.gz", fastaGZ, resources.TransformGzipFASTA, "hg38.fa"),
		res("hg19_fasta", "/hg19.fa.gz", fastaGZ, resources.TransformGzipFASTA, "hg19.fa"),
		res("hg38_to_hg19_chain", "/chain.gz", chain, resources.TransformNone, ""),
		res("hg38_aliases", "/aliases.txt", aliases, resources.TransformNone, ""),
	}}

	input := filepath.Join(d, "sample.g.vcf.gz")
	writeGzipText(t, input, "##fileformat=VCFv4.2\n##contig=<ID=chr1,length=248956422>\n##ALT=<ID=NON_REF,Description=\"Represents any possible alternative allele at this location\">\n##INFO=<ID=END,Number=1,Type=Integer,Description=\"End position\">\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\tFORMAT\tS1\nchr1\t2\t.\tC\tT,<NON_REF>\t.\tPASS\t.\tGT\t0/1\n")
	output := filepath.Join(d, "sample.hg19.vcf.gz")

	c := NewNative(filepath.Join(d, "cache"))
	c.Manifest = manifest
	c.Installation = engine.Installation{BCFTools: fake}
	c.GATK = gatk.Installation{}
	c.GATKManager.Catalog = gatk.Catalog{
		GATKVersion: "test", JavaVersion: "17-test",
		GATK: gatk.Asset{Name: "GATK test", GitHubReleaseAPI: srv.URL + "/gatk-release", GitHubAssetName: "gatk.zip", Filename: "gatk.zip", Archive: gatk.ArchiveZip},
		Java: map[string]gatk.Asset{gatk.PlatformKey(): {Name: "Java test", URL: srv.URL + "/java.zip", ChecksumURL: srv.URL + "/java.sha256", Filename: "java.zip", Archive: gatk.ArchiveZip}},
	}

	result, err := c.Convert(context.Background(), model.JobConfig{InputPath: input, OutputPath: output}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != model.ModeGVCFGenotypeThenLift {
		t.Fatalf("mode=%s", result.Mode)
	}
	if result.LiftedVariants != 1 {
		t.Fatalf("lifted=%d", result.LiftedVariants)
	}
}

type testZipEntry struct {
	Body string
	Mode os.FileMode
}

func writeTestZip(t *testing.T, path string, entries map[string]testZipEntry) {
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

func writeGzipText(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

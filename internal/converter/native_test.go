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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/emre-tarhan/vcflift/internal/engine"
	"github.com/emre-tarhan/vcflift/internal/gatk"
	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/resources"
)

func TestNativeConverterGRCh37TargetProfile(t *testing.T) {
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
out=""
infile=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  case "$a" in
    *.vcf|*.vcf.gz) if [ -f "$a" ] && [ "$a" != "$out" ]; then infile="$a"; fi ;;
  esac
  prev="$a"
done
case "$cmd" in
  index)
    last=""
    for a in "$@"; do last="$a"; done
    : > "$last.tbi"
    exit 0
    ;;
  sort|view)
    if [ -n "$out" ]; then
      if [ -n "$infile" ]; then gzip -c "$infile" > "$out"; else gzip -c > "$out"; fi
    else
      if [ -n "$infile" ]; then cat "$infile"; else cat; fi
    fi
    exit 0
    ;;
  +liftover)
    cat
    exit 0
    ;;
  norm|annotate)
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
	vcfText := "##fileformat=VCFv4.2\n##contig=<ID=chr1,length=248956422>\n##contig=<ID=chrM,length=16569>\n##contig=<ID=chrUn_gl000220v1,length=161802>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nchr1\t2\t.\tC\tT\t.\tPASS\t.\nchrM\t5\t.\tT\tC\t.\tPASS\t.\nchrUn_gl000220v1\t9\t.\tG\tA\t.\tPASS\t.\n"
	if err := os.WriteFile(input, []byte(vcfText), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(d, "sample.hg19.vcf.gz")

	c := NewNative(filepath.Join(d, "cache"))
	c.Manifest = manifest
	c.Installation = engine.Installation{BCFTools: fake}
	result, err := c.Convert(context.Background(), model.JobConfig{
		InputPath: input, OutputPath: output,
		TargetProfile: "grch37-primary", KeepRejected: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetProfile != "grch37-primary" {
		t.Fatalf("target profile=%q", result.TargetProfile)
	}
	if result.LiftedVariants != 1 {
		t.Fatalf("lifted=%d want 1 (chr1 only, post-profile)", result.LiftedVariants)
	}
	if result.LiftoverInputVariants != 3 {
		t.Fatalf("liftover input=%d want 3 (conservation counts pre-profile)", result.LiftoverInputVariants)
	}
	if result.ProfileRejects["stale_hg19_chrM"] != 1 || result.ProfileRejects["non_primary_contig"] != 1 {
		t.Fatalf("profile rejects=%v", result.ProfileRejects)
	}

	outText := readGzipText(t, output)
	if !strings.Contains(outText, "##vcflift_target_profile=grch37-primary") || !strings.Contains(outText, "##contig=<ID=1,length=249250621>") {
		t.Fatalf("profiled output header wrong:\n%s", outText)
	}
	if !strings.Contains(outText, "\n1\t2\t") || strings.Contains(outText, "chrM\t") || strings.Contains(outText, "chrUn") {
		t.Fatalf("profiled output records wrong:\n%s", outText)
	}

	rejPath := result.ProfileRejectPath
	if rejPath == "" {
		t.Fatal("profile reject path missing")
	}
	rejText := readGzipText(t, rejPath)
	if !strings.Contains(rejText, "\nchrM\t5\t.\tT\tC\t.\tstale_hg19_chrM\t") || !strings.Contains(rejText, "\nchrUn_gl000220v1\t9\t.\tG\tA\t.\tnon_primary_contig\t") {
		t.Fatalf("profile reject bucket wrong:\n%s", rejText)
	}

	reportBytes, err := os.ReadFile(output + ".report.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		TargetProfile  string           `json:"target_profile"`
		ProfileRejects map[string]int64 `json:"profile_rejects"`
	}
	if err := json.Unmarshal(reportBytes, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.TargetProfile != "grch37-primary" || doc.ProfileRejects["stale_hg19_chrM"] != 1 {
		t.Fatalf("report doc wrong: %+v", doc)
	}
}

func readGzipText(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNativeConverterReverseHG19ToHG38(t *testing.T) {
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
        *.vcf|*.vcf.gz) if [ -f "$a" ]; then infile="$a"; fi ;;
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
	aliases := []byte("chr1\t1\tNC_000001.10\n")
	files := map[string][]byte{
		"/hg38.fa.gz": fastaGZ, "/hg19.fa.gz": fastaGZ, "/chain.gz": chain, "/aliases.txt": aliases,
		"/chain19to38.gz": chain, "/hg19aliases.txt": aliases,
	}
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
		res("hg19_to_hg38_chain", "/chain19to38.gz", chain, resources.TransformNone, ""),
		res("hg38_aliases", "/aliases.txt", aliases, resources.TransformNone, ""),
		res("hg19_aliases", "/hg19aliases.txt", aliases, resources.TransformNone, ""),
	}}

	input := filepath.Join(d, "old.vcf")
	vcfText := "##fileformat=VCFv4.2\n##contig=<ID=chr1,length=249250621>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\nchr1\t2\t.\tC\tT\t.\tPASS\t.\n"
	if err := os.WriteFile(input, []byte(vcfText), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(d, "old.hg38.vcf.gz")

	c := NewNative(filepath.Join(d, "cache"))
	c.Manifest = manifest
	c.Installation = engine.Installation{BCFTools: fake}
	result, err := c.Convert(context.Background(), model.JobConfig{InputPath: input, OutputPath: output}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Direction != model.DirectionReverse {
		t.Fatalf("direction=%s", result.Direction)
	}
	if result.LiftedVariants != 1 {
		t.Fatalf("lifted=%d", result.LiftedVariants)
	}
	for _, p := range []string{output, output + ".tbi", output + ".report.json"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
	var doc struct {
		SourceAssembly string `json:"source_assembly"`
		TargetAssembly string `json:"target_assembly"`
	}
	reportBytes, err := os.ReadFile(output + ".report.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reportBytes, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SourceAssembly != "hg19" || doc.TargetAssembly != "hg38" {
		t.Fatalf("assemblies=%s->%s", doc.SourceAssembly, doc.TargetAssembly)
	}
}

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

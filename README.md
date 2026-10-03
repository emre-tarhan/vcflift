# VCF Lift

**Canonical repository:** `https://github.com/emre-tarhan/vcflift`  
**Go module:** `github.com/emre-tarhan/vcflift`

VCF Lift is a local desktop + CLI application for **allele-aware hg38 → UCSC hg19 conversion** of VCF and supported single-sample gVCF inputs.

> **Status:** `v0.9.0` / v1.0 release-hardening. The DeepVariant 1.10.0 `<*>` gVCF path has passed a real 50.6M-record end-to-end run with exact candidate conservation, source/target REF validation, placeholder QC and indexed output. Ordinary VCF and GATK/HaplotypeCaller gVCF real-data release gates remain.

## Normal use

A normal CLI conversion is one command:

```bash
vcflift-cli convert \
  --accept-ucsc-license \
  --keep-rejects \
  sample.hg38.vcf.gz
```

VCF Lift handles input inspection, native engine setup, reference preparation, cache reuse, gVCF dialect selection, hg38 REF validation, allele-aware liftover, hg19 REF validation, BGZF/TBI output and QC reporting.

The desktop app exposes the same pipeline as a focused workflow: **choose/drop file → inspect → convert → review result**. It uses a light-only, high-contrast desktop theme. If required references are missing, a first-run setup dialog opens automatically; downloads and local preparation are shown per resource instead of behind one ambiguous progress bar. Reference/cache management lives in a separate Resources area rather than in the primary conversion flow.

Genotype records are processed locally. VCF Lift has no telemetry and does not upload sample data.

## First-use resources

Large resources are downloaded only when needed and are cached locally:

- Linux: normally `~/.cache/vcflift`
- Windows: normally `%LOCALAPPDATA%\VCFLift`

Reference bootstrap now:

- downloads independent resources concurrently (default maximum: 3);
- resumes `.part` downloads with HTTP Range requests;
- verifies checksums before activation;
- performs a free-disk preflight before large reference preparation;
- deletes compressed FASTA archives after verified `.fa + .fai + .dict` preparation by default;
- reuses prepared assets on later conversions.

For GATK/HaplotypeCaller gVCF, the pinned Java 17 and GATK downloads also start concurrently on first use.

Useful cache commands:

```bash
vcflift-cli cache status
vcflift-cli cache clean          # safe cleanup; preserves prepared refs/runtimes
vcflift-cli cache clean --all    # destructive; everything will need preparation again
```

See [`docs/BOOTSTRAP_CACHE.md`](docs/BOOTSTRAP_CACHE.md).

## Build from source

### Linux / WSL2 native build

```bash
./build.sh --install-deps --clean
```

Produces:

```text
dist/vcflift-cli
dist/VCFLift
```

CLI-only development:

```bash
./build.sh --cli-only
```

Fast development iteration (skips module verify + tests, seconds on a warm Go cache):

```bash
./build.sh --dev --gui
```

### Windows `.exe` from Linux / WSL2

Yes — VCF Lift can be cross-compiled to Windows from WSL2/Linux. Fyne requires MinGW-w64 because its desktop backend uses CGO, and a portable Windows build must embed a **Windows** bcftools/liftover engine payload (the Linux engine cannot run inside a Windows executable).

The Windows application can be cross-compiled in WSL/Linux, but it needs a verified Windows native engine payload once. After pushing the repository to GitHub, run the **Native engine builds** workflow and fetch its Windows artifact:

```bash
./scripts/fetch-windows-engine.sh
```

The helper stores the reusable bundle at `artifacts/vcflift-engine-windows-amd64.zip`. Then build Windows from WSL/Linux with:

```bash
./build.sh --windows --install-deps
```

Produces:

```text
dist/VCFLift-windows-amd64.exe
dist/vcflift-cli-windows-amd64.exe
```

Fast Windows GUI cross iteration (engine already fetched once):

```bash
./build.sh --dev --gui --windows
```

To build Linux and Windows artifacts in one WSL/Linux run after the engine artifact has been fetched once:

```bash
./build.sh --all --install-deps --clean
```

You can still override the auto-detected artifact with `--windows-engine PATH`.

Native Windows developers can alternatively use `build.ps1`.

Older development ZIPs omitted `go.sum`. A full GUI/cross build runs `go mod tidy` once when needed; **keep/commit the resulting `go.sum`** for the v1.0 source tree.

See [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md).

## Supported conversion paths

### Ordinary VCF

```text
hg38 VCF
 → optional chromosome alias normalization
 → hg38 REF validation
 → bcftools +liftover
 → hg19 REF validation
 → sort / BGZF / TBI
 → QC report
```

### DeepVariant `<*>` gVCF

DeepVariant has finalized genotype calls in the gVCF. VCF Lift therefore does **not** run GATK `GenotypeGVCFs` for this dialect.

```text
DeepVariant hg38 gVCF
 → retain GT="alt" finalized calls
 → trim <*>/<NON_REF> confidence alleles
 → hg38 REF validation
 → allele-aware liftover
 → hg19 REF validation
 → sort / BGZF / TBI
 → QC report
```

The validated real-data checkpoint is:

```text
source non-reference calls    5,422,963
liftover input                5,422,963
lifted                        4,942,650
rejected                        480,313
candidate conservation        PASS
output <*>                    0
output <NON_REF>              0
```

### GATK/HaplotypeCaller `<NON_REF>` gVCF

HaplotypeCaller gVCF is a reference-confidence intermediate, so source genotyping is required:

```text
GATK hg38 gVCF
 → validate/reuse or create source index
 → hg38 representation-safe REF check
 → GATK GenotypeGVCFs on hg38
 → ordinary hg38 VCF
 → allele-aware liftover
 → hg19 validation / BGZF / TBI / QC
```

The managed runtime is pinned to GATK `4.7.0.0` and Eclipse Temurin JRE `17.0.20.1+1`.

See [`docs/GVCF.md`](docs/GVCF.md).

## Rejects are auditable

Keep the rejected VCF:

```bash
vcflift-cli convert --accept-ucsc-license --keep-rejects sample.vcf.gz
```

Analyze any reject VCF without rerunning liftover:

```bash
vcflift-cli reject-audit sample.hg19.vcf.gz.rejected.vcf.gz
```

For development research, `scripts/reject-rescue-benchmark.sh` reruns **only the reject set** through a conservative `--max-snp-gap` / `--max-indel-inc` matrix and requires hg19 REF validation. On the validated 480,313-record reject set, the strongest tested rescue (`max-snp-gap=10`) recovered only 165 records (0.034353%); production defaults therefore remain unchanged. The benchmark now writes source-aware `*.rescued.tsv` files for allele/coordinate audit.

See [`docs/REJECTS.md`](docs/REJECTS.md).

## Release targets

v1.0 targets:

- Linux x64 GUI + CLI
- Windows x64 GUI `.exe` + CLI `.exe`
- embedded platform-native bcftools 1.24 + liftover engine
- lazy reference/runtime bootstrap with resume/checksums
- machine-readable QC + explicit rejected variants
- local-only genotype processing

Current release gates are tracked in [`docs/RELEASE_CHECKLIST.md`](docs/RELEASE_CHECKLIST.md).

## Project documentation

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- [`docs/BOOTSTRAP_CACHE.md`](docs/BOOTSTRAP_CACHE.md)
- [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md)
- [`docs/ENGINE_BUNDLES.md`](docs/ENGINE_BUNDLES.md)
- [`docs/GVCF.md`](docs/GVCF.md)
- [`docs/GUI.md`](docs/GUI.md)
- [`docs/REJECTS.md`](docs/REJECTS.md)
- [`docs/VALIDATION.md`](docs/VALIDATION.md)
- [`docs/RELEASE_CHECKLIST.md`](docs/RELEASE_CHECKLIST.md)

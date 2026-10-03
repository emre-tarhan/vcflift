# VCF Lift

**Canonical repository:** `https://github.com/emre-tarhan/vcflift`  
**Go module:** `github.com/emre-tarhan/vcflift`

VCF Lift is a local desktop + CLI application for **allele-aware conversion between hg38 and hg19/GRCh37** in either direction: forward hg38 → UCSC hg19 (the validated default) and reverse hg19/GRCh37 → hg38. Supported single-sample gVCF inputs convert to variant VCFs: every output is a **variant VCF — never a gVCF**; joint genotyping happens on the source assembly, before liftover.

> **Status:** `v1.1.1`. The GIAB HG002 v4.2.1 benchmark gates passed in both directions (98.85% forward, 98.74% reverse cross-check concordance after left-aligned normalization; high-confidence benchmark regions, chr1–22, MT excluded), and the `grch37-primary` naming profile gate matched the official GRCh37 benchmark at 99.10%. Separately, a real 50.6M-record DeepVariant cohort gVCF converts with 8.9% of candidates rejected in chain gaps — a production callset, not a high-confidence benchmark; reject rates scale with the input's distance from the chain. The GATK gVCF path is gated on a public GATK-produced exome gVCF. See `docs/VALIDATION.md`.

## Normal use

A normal CLI conversion is one command:

```bash
vcflift-cli convert \
  --accept-ucsc-license \
  --keep-rejects \
  sample.hg38.vcf.gz
```

VCF Lift handles input inspection, native engine setup, reference preparation, cache reuse, gVCF dialect selection, source REF validation, allele-aware liftover, target REF validation, BGZF/TBI output and QC reporting.

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

Windows builds are not code-signed, so SmartScreen may show a "Windows protected your PC" prompt on first run: choose **More info → Run anyway**. The `.exe` carries embedded version metadata (visible in Explorer → Properties → Details).

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

DeepVariant has finalized genotype calls in the gVCF. VCF Lift therefore does **not** run GATK `GenotypeGVCFs` for this dialect. The output is an hg19 variant VCF: finalized variant calls are lifted, while reference-confidence blocks are dropped by design. The QC report counts the dropped source records in `gvcf_blocks_dropped`.

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

The managed runtime is pinned to GATK `4.7.0.0` and Eclipse Temurin JRE `17.0.20.1+1`. The output is an hg19 variant VCF, not an hg19 gVCF: reference-confidence territory is consumed by GenotypeGVCFs on hg38 and is not carried across liftover (the QC report records `output_class: "variant_vcf"` and `gvcf_blocks_dropped`).

See [`docs/GVCF.md`](docs/GVCF.md).

## Target naming profiles

The liftover always targets UCSC hg19; the output naming is selectable (CLI `--target-profile`, GUI Output section):

- `ucsc-hg19` (default): chr-prefixed UCSC naming, unchanged validated output.
- `grch37-primary`: primary contigs renamed to GRCh37 naming (`1`–`22`, `X`, `Y`), header dictionary rebuilt from the GRCh37 primary assembly. `chrM` records are rejected with `stale_hg19_chrM` (hg19 chrM is the old NC_001807 sequence; the GRCh37 MT is the rCRS — coordinates cannot be derived by renaming). Records lifted to unplaced/unlocalized/alt contigs are rejected with `non_primary_contig`. Both land in a separate `.profile-rejected.vcf.gz` bucket and are counted in the QC report.
- `hs37d5`: GRCh37 primary naming plus an explicit header note that decoy contigs are not produced.

`--grch37-fasta PATH` optionally verifies every output REF base against a user-supplied faidx-indexed GRCh37 FASTA (pure comparison, no normalization). See [`docs/TARGET_PROFILES.md`](docs/TARGET_PROFILES.md).

## Reverse conversion (hg19 → hg38)

An input detected as hg19 or GRCh37 converts in the opposite direction automatically — same engine, same REF validation on both sides, same reject auditing:

```bash
vcflift-cli convert --accept-ucsc-license old.hg19.vcf.gz   # → old.hg38.vcf.gz
```

- UCSC hg19 (`chr`-prefixed) and GRCh37/b37 primary naming (`1`–`22`, `X`, `Y`, `MT`) are both accepted; GRCh names are normalized to UCSC before liftover.
- First reverse use downloads the UCSC hg19→hg38 chain (same UCSC license terms) plus hg19 chromosome aliases.
- DeepVariant-style gVCFs are supported through candidate extraction like forward. GATK-style hg19 gVCFs are rejected with guidance: run GenotypeGVCFs against your own hg19 reference first, then convert the resulting VCF (the managed runtime is hg38-only).
- Target naming profiles apply to the forward direction only.

Reverse passed its own real-data gate: the GIAB HG002 GRCh37 v4.2.1 benchmark (4.03M records) converts with 222 rejects, zero REF mismatches on both sides, and **98.74% exact concordance** with the official GRCh38 benchmark after left-aligned normalization (`docs/VALIDATION.md`, gate 5).

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
- [`docs/TARGET_PROFILES.md`](docs/TARGET_PROFILES.md)
- [`docs/VALIDATION.md`](docs/VALIDATION.md)
- [`docs/RELEASE_CHECKLIST.md`](docs/RELEASE_CHECKLIST.md)
- [`docs/ROADMAP.md`](docs/ROADMAP.md)

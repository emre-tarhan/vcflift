# Architecture

```text
Fyne GUI ─┐
          ├── internal/converter
CLI ──────┘          │
                     ├── VCF/gVCF inspection + planning
                     ├── resource manager
                     │      ├── resumable downloads
                     │      ├── checksum verification
                     │      ├── FASTA decompression
                     │      ├── Go-native .fai
                     │      └── Go-native .dict
                     │
                     ├── internal/enginebundle
                     │      ├── embedded platform payload
                     │      ├── portable engine .zip import/install
                     │      ├── SHA-256 manifest
                     │      ├── atomic extraction
                     │      └── cache corruption repair
                     │
                     ├── internal/engine
                     │      ├── BCFtools preflight
                     │      ├── existing TBI/CSI readability check
                     │      ├── liftover capability check
                     │      ├── shell-free os/exec runner
                     │      └── VCF/gVCF process plans
                     │
                     ├── internal/gatk
                     │      └── GenotypeGVCFs runtime preflight
                     │
                     └── JSON QC/reporting
```

The UI contains no genomics implementation. GUI and CLI use the same converter core. The desktop layer is light-only and presents macro workflow states rather than inventing a linear percentage for the streaming native pipeline. Reference readiness is read through `internal/resources.Manager.Status`; source preparation itself remains owned by the same resource manager used by CLI conversion.

## Standard VCF path

```text
inspect
 -> optional chromosome alias normalization
 -> hg38 REF validation
 -> BCFtools/liftover
 -> hg19 REF validation
 -> sort/BGZF
 -> TBI
 -> JSON report
```

## GATK gVCF path (default)

```text
inspect
 -> discover adjacent TBI/CSI
 -> validate and reuse index when safe
 -> optional alias normalization
 -> source hg38 REF validation with norm -N
 -> stage/index only when reuse is unsafe
 -> GATK GenotypeGVCFs(hg38)
 -> ordinary hg38 VCF
 -> source REF validation
 -> BCFtools/liftover
 -> hg19 REF validation
 -> sort/BGZF/TBI
 -> JSON report
```

The source reference cache includes both `.fai` and `.dict`, allowing GenotypeGVCFs to use the same verified UCSC hg38 FASTA as the BCFtools stages.


## Input index policy

For BGZF-compressed gVCF input, an adjacent `.tbi` or `.csi` is considered reusable only when it is not older than the gVCF and `bcftools index -n` can read it. If contig naming must change, the original index cannot describe the transformed stream, so VCF Lift creates a temporary staged gVCF and a matching temporary index. The original input pair is never modified.

When a sidecar is reused, source REF validation uses `bcftools norm -N -f <hg38> -c e`; `-N` is intentional so validation does not left-align or otherwise rewrite the source gVCF before `GenotypeGVCFs`.

## Advanced gVCF candidate path

```text
inspect
 -> optional alias normalization
 -> trim <NON_REF>/<*>
 -> finalized GT="alt" records (including sequence-resolved TYPE=OTHER calls)
 -> hg38 REF validation
 -> liftover
 -> hg19 REF validation
 -> output
```

This is not the recommended final-call path.

## Embedded BCFtools engine

Development source stores no native third-party executable. CI builds a platform payload under:

```text
internal/enginebundle/payload/<goos>-<goarch>/
```

before the final Go application is compiled. `go:embed` captures that payload. Its manifest records:

- bundle format version
- engine version
- platform
- BCFtools version
- exact resolved `freeseek/score` source ref
- relative executable/plugin paths
- SHA-256 for every file

At runtime a verified bundle is extracted under the cache. Any checksum mismatch causes re-extraction rather than execution of a modified engine. Development builds can also install the exact same manifest format from a standalone engine `.zip`.

Engine resolution order is deterministic:

```text
explicit override
 -> embedded pinned release engine
 -> verified installed engine bundle
 -> compatible PATH fallback
```

The v1 profile rejects engines whose BCFtools version is not exactly `1.24`.

### Windows

Windows engine CI follows the upstream BCFtools MSYS2/MINGW64 build model. Local engine import uses Go's PE parser to walk the `bcftools.exe`/`liftover.so` imported-library graph recursively, bundle non-system MinGW DLLs, and leave Windows system DLLs on the host. The plugin stays under `plugins/liftover.so`.

### Linux

Linux builds currently target the GitHub `ubuntu-22.04` baseline. A future AppImage layer can further reduce host-library variability; the embedded-engine mechanism does not need to change for that.

## GATK runtime

The scientifically preferred gVCF path uses a lazily installed, pinned Java 17 + GATK runtime when a valid external runtime is not supplied. Ordinary VCF users never download this runtime.

## Resources

The exact hg38 -> hg19 profile uses UCSC hg38/hg19 FASTA and UCSC `hg38ToHg19.over.chain.gz`. The chain is downloaded at runtime rather than redistributed. Reference downloads are checksum-verified and cached.

`internal/resources.Manager` prepares independent assets with bounded concurrency (three workers by default), keeps Range-resumable `.part` transfers, performs a conservative free-space preflight, and removes source FASTA `.gz` archives after verified `.fa/.fai/.dict` preparation unless archive retention is explicitly requested. `Manager.Status`/`AllReady` expose filesystem readiness without network access so the GUI can render first-run setup and per-resource Download/Prepare states. This reduces first-use wall-clock time without changing the scientific resource set. Cache inspection/cleanup lives in `internal/cache`; disk-space probing is platform-specific under `internal/diskspace`.

## Lazy GATK runtime

`internal/gatk` owns the optional gVCF runtime. `converter.NativeConverter` first validates any explicit/system installation. If no complete compatible pair exists, it calls `gatk.Manager.Prepare`. The manager downloads only for `gvcf_genotype_then_lift`, starts missing Java and GATK archive downloads concurrently, validates SHA-256 before extraction, rejects archive path traversal, caches Java and the GATK local JAR, then runs the same `gatk.Validate` preflight used for external installations.

Current pins:

```text
GATK:    4.7.0.0
Java:    Eclipse Temurin JRE 17.0.20.1+1
Targets: windows-amd64, linux-amd64
```

The GATK archive is resolved from the exact GitHub release tag and its release-asset `digest`; Temurin uses the vendor-provided SHA-256 sidecar. This runtime remains separate from the embedded BCFtools engine because most VCF users never need it.

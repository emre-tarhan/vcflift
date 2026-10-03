# Bootstrap and cache behavior

VCF Lift keeps large external assets outside release binaries and prepares them lazily in a local cache.

## Cache locations

Default cache root:

- Linux: `~/.cache/vcflift`
- Windows: `%LOCALAPPDATA%\VCFLift`

Main subdirectories:

```text
engine/       extracted verified native bcftools/liftover payload
resources/v1 prepared hg38/hg19 FASTA, indexes, dictionary, chain, aliases
runtime/v1   managed Java 17 + GATK (only required by GATK-style gVCF)
```

## Parallel first-use preparation

Reference resources are independent and are prepared with bounded concurrency. The default maximum is three workers. Downloads remain resumable and checksum-verified.

The implementation uses bounded rather than unlimited concurrency so a first run does not create an arbitrary number of simultaneous large transfers or decompression jobs.

For GATK-style gVCF, Java and GATK archive downloads also run concurrently before installation.


## Desktop first-run behavior

The desktop app checks local reference readiness at startup. When required reference data is missing, a first-run setup dialog opens automatically instead of leaving the user on a conversion screen that cannot yet complete. Starting setup opens the Resources workspace.

Resources are displayed independently with separate **Download** and **Prepare** states. FASTA downloads can therefore show `VERIFIED` while decompression/index/dictionary work shows `PREPARING`; chain/alias resources report preparation as not required. Per-resource download bars replace the old single global spinner.

When all required references are ready, the large setup call-to-action is demoted to a small `Check & repair` action. Normal users never need to install or select a development engine bundle; the platform-native engine is embedded and chosen automatically.

## Disk-space preflight

Before missing hg38/hg19 resources are prepared, VCF Lift estimates peak cache workspace from the expected compressed downloads plus prepared FASTA sizes and an additional safety margin. If the cache filesystem clearly lacks that space, preparation stops before multi-gigabyte work begins.

The estimate is conservative; it is a preflight guard, not an exact final-cache-size promise.

## Archive retention

Prepared references need:

```text
hg38.fa + hg38.fa.fai + hg38.dict
hg19.fa + hg19.fa.fai + hg19.dict
```

The original `hg38.fa.gz` / `hg19.fa.gz` downloads are not required after successful preparation. They are deleted by default to reduce persistent disk footprint. GATK/Java download archives are likewise removed after verified installation.

## Cleanup

```bash
vcflift-cli cache status
vcflift-cli cache clean
```

Safe cleanup removes stale `.part` files, leftover runtime archives and compressed FASTA archives whose prepared sidecars exist. It preserves prepared references, chain/aliases, installed runtimes and engine data.

`cache clean --all` removes the entire VCF Lift cache and should be treated as destructive.

## Download interruption

Downloads write to `.part` paths. On a later attempt, HTTP Range requests resume from the partial size when supported. The final filename is activated only after the transfer completes; configured checksums are verified before the resource is accepted.

## Multiple application instances

Cross-process locking: reference preparation, engine installation, GATK runtime preparation and cache cleaning each take an exclusive `.lock` file lock on the target directory (`internal/cachelock`). A second concurrent process fails fast with a clear message instead of racing the first one on a half-written cache.

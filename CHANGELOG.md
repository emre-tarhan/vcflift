# Changelog

## Unreleased

- Reverse conversion hg19/GRCh37 → hg38: inputs detected as hg19 (UCSC or GRCh37/b37 primary naming) now convert in the opposite direction automatically — same allele-aware engine, REF validation against hg19 before and hg38 after liftover, same reject auditing, `.hg38.vcf.gz` output naming. First reverse use downloads the UCSC hg19→hg38 chain (same license terms) and hg19 chromosome aliases. DeepVariant-style gVCFs are supported through candidate extraction; GATK-style hg19 gVCFs are rejected with guidance (managed GenotypeGVCFs runtime is hg38-only). Target naming profiles remain forward-only. The forward hg38→hg19 pipeline is unchanged.
- Assembly detection now uses a majority contig-length vote: hs37d5/GRCh37 headers carry the rCRS MT (16569), which collides with the hg38 MT length; previously such headers were undetectable and silently defaulted to forward.
- Reverse real-data gate (GIAB HG002 v4.2.1 GRCh37 benchmark, 4.03M records): 4,033,574 lifted / 222 rejected, zero REF mismatches on both sides, 98.74% exact concordance with the official GRCh38 benchmark after left-aligned normalization (docs/VALIDATION.md gate 5).
- Target naming profiles (`--target-profile`, GUI Output section): `ucsc-hg19` default unchanged; `grch37-primary` renames primary contigs to GRCh37 naming with a rebuilt GRCh37 primary header dictionary, rejects chrM records as `stale_hg19_chrM` (hg19 chrM = NC_001807, GRCh37 MT = rCRS; not renamable) and non-primary-contig records as `non_primary_contig`, both into a separate `.profile-rejected.vcf.gz` bucket counted in the QC report (`target_profile`, `profile_rejects`); `hs37d5` adds the explicit "decoy contigs are not produced" header note. Profiles are a post-liftover layer; the validated pipeline is untouched.
- Optional second REF check: `--grch37-fasta PATH` compares every GRCh37-profiled output REF base against a user-supplied faidx-indexed GRCh37 FASTA in-process (no normalization, no download).
- `grch37-primary` real-data gate (GIAB HG002 v4.2.1): 4,043,025 lifted, liftover rejects identical to the ucsc-hg19 baseline, 342 non-primary drops; 99.10% exact concordance with the official GRCh37 benchmark after left-aligned normalization (docs/VALIDATION.md gate 4).
- Output contract is now explicit in three places: the GUI shows a one-sentence contract when a gVCF is selected ("the output will be an hg19 variant VCF, not an hg19 gVCF"); the QC report records `output_class: "variant_vcf"` on every conversion and `gvcf_blocks_dropped` (source gVCF records not carried into the output) for gVCF inputs; README/docs gVCF sections state the contract instead of implying gVCF-to-gVCF conversion.
- Honesty caveat on the 98.85% cross-check headline in README/CHANGELOG: chr names reconciled, left-aligned normalization, GIAB chr1–22 benchmark, MT excluded (full method in docs/VALIDATION.md).

## v1.0.0 (2026-10-03)

First stable release. All three conversion paths have passed real-data release gates (see docs/VALIDATION.md).

- Windows: child processes (bcftools, java) no longer open console windows during GUI conversions.
- GATK gVCF gate passed end-to-end on a public GATK-produced gVCF (pinned Java/GATK runtime first-use, GenotypeGVCFs, liftover, full QC).
- Network hardening: first-use downloads retry transient failures (4 attempts, exponential backoff with jitter) and resume from `.part` state.
- Windows: real-machine smoke test passed (conversion, hover styling, console-free child processes).
- Windows known gap: the managed Java/GATK first-use download has been exercised on Linux only; the code path is shared, but a native Windows run is still pending.
- Real-data gates for v1.0: the v0.9.0 build reproduces the validated DeepVariant gVCF run bit-for-bit; an ordinary-VCF gate on the GIAB HG002 v4.2.1 benchmark passes with 98.85% independent GRCh37 cross-check concordance (chr names reconciled, left-aligned normalization, GIAB chr1–22 benchmark, MT excluded).
- Cross-process cache locking: reference preparation, engine installation, GATK runtime preparation and cache cleaning take an exclusive advisory lock and fail fast with a clear message when another VCF Lift process is already working on the same cache.

## v0.9.0 — first public release (2026-10-03)

VCF Lift converts VCF and gVCF files locally from hg38 to hg19: allele-aware liftover through a pinned native engine, explicit REF validation on both the source and the target build, explicit reject records for unmappable variants and a machine-readable QC report. Nothing leaves the computer.

### Added

- Desktop GUI (Fyne, Linux and Windows) and a CLI sharing the same conversion core: file inspection, stage-based conversion progress, per-resource reference setup, first-run onboarding.
- Embedded native engine per platform (bcftools 1.24 plus the `freeseek/score` liftover plugin), resolved in a deterministic order: embedded pinned engine, then a verified installed bundle, then PATH.
- DeepVariant gVCF support: finalized `GT="alt"` calls are lifted directly; reference-confidence blocks and `<*>`/`<NON_REF>` placeholders are excluded from the variant output.
- GATK gVCF support: `GenotypeGVCFs` runs against hg38 first, using a pinned Eclipse Temurin Java 17 runtime and GATK 4.7.0.0 that are prepared into the application cache on first use.
- Reference bootstrap: bounded parallel downloads, resumable `.part` transfers, checksum verification, free-disk preflight and safe cache cleanup (`vcflift-cli cache status|clean|clean --all`).
- Auditable rejects: every unmappable record is written to a sidecar VCF with its reason; `reject-audit` and a research-only rescue benchmark are included.
- Windows: child processes run without console windows; portable builds embed the Windows engine payload.
- Build tooling: `build.sh` with fast development targets (`--dev`, `--gui`, `--windows`), a Windows engine fetch helper, and a CI release pipeline that publishes tagged releases with SHA256SUMS.

### Validation

- DeepVariant 1.10.0 hg38 real gVCF, 50,574,105 input records and 5,422,963 candidate calls: 4,942,650 lifted (91.143%), 480,313 rejected with exact candidate conservation, source and target REF validation, placeholder checks and indexed output all passing. See `docs/VALIDATION.md`.

### Known limitations

- The ordinary-VCF and GATK gVCF code paths are implemented but have not yet passed real-data end-to-end release gates; DeepVariant gVCF is the validated path.
- The cache has no cross-process locking yet: do not start two first-time reference setups against the same cache at once.
- Reject rescue stays at plugin defaults on purpose; the strongest tested parameter sweep recovered 0.035% of rejects (`docs/REJECTS.md`).

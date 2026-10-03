# Changelog

## Unreleased

- Windows: child processes (bcftools, java) no longer open console windows during GUI conversions.
- GATK gVCF gate passed end-to-end on a public GATK-produced gVCF (pinned Java/GATK runtime first-use, GenotypeGVCFs, liftover, full QC).
- Real-data gates for v1.0: the v0.9.0 build reproduces the validated DeepVariant gVCF run bit-for-bit; an ordinary-VCF gate on the GIAB HG002 v4.2.1 benchmark passes with 98.85% independent GRCh37 cross-check concordance.
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

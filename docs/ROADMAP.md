# Roadmap

This document exists because v0.9.0 and v1.0.0 were published on the same day (2026-10-03). That compressed signal weakened what a 1.0 usually communicates: "this shape is stable". The compensation is an explicit, dated roadmap and a strict patch discipline — so version numbers carry meaning again.

## Version discipline

- **Patch (v1.0.x / v1.1.x)**: bug fixes only. No new flags, no output-format changes, no resource additions. A patch must not change any byte of a reproducible conversion.
- **Minor (v1.x)**: additive capability (new directions, profiles, report fields) with the validated hg38→hg19 pipeline and its production defaults held fixed. Breaking CLI/QC-contract changes require a major version.
- **Gate rule**: every conversion-affecting feature ships with a real-data gate recorded in `docs/VALIDATION.md` before release. No gate, no release.

## Shipped

### v1.0.0 (2026-10-03)

- Validated hg38 → UCSC hg19 conversion, three paths gated on real data: ordinary VCF (GIAB HG002 v4.2.1, 99.88% lifted, 98.85% independent GRCh37 cross-check), DeepVariant `<*>` gVCF (50.6M-record cohort run, bit-for-bit reproducible), GATK `<NON_REF>` gVCF (pinned GenotypeGVCFs runtime).
- Embedded bcftools 1.24 + liftover engine, auditable rejects, machine-readable QC, Linux + Windows desktop/CLI.

### v1.1.0 (2026-10-03)

- **Explicit output contract** (P0): GUI, QC report (`output_class`, `gvcf_blocks_dropped`) and docs state that every gVCF conversion produces a variant VCF, never a gVCF.
- **Target naming profiles**: `grch37-primary` (GRCh37 primary naming, chrM rejected as `stale_hg19_chrM`, non-primary contigs as `non_primary_contig`, separate reject bucket), `hs37d5` naming note. Gated at 99.10% exact concordance with the official GRCh37 benchmark.
- **Reverse conversion hg19/GRCh37 → hg38**: automatic direction detection (majority contig-length vote, hs37d5 MT collision handled), GRCh/b37 naming accepted, UCSC hg19→hg38 chain + hg19 aliases managed as resources. Gated at 98.74% exact concordance with the official GRCh38 benchmark, zero REF mismatches on both sides.
- Assembly detection hardened; forward pipeline unchanged step-for-step.

## Next

- **v1.1.x patches**: whatever the field reports; Windows first-use Java/GATK download exercise on real Windows hardware (code path shared with the validated Linux runs).
- **Candidate v1.2 scope** (not committed until gated): reverse-direction gVCF candidate extraction on real data; multi-sample VCF behavior documented and gated; `grch38-primary` naming profile if requested.
- Explicitly out of scope for the v1 line: T2T targets, SV liftover (separate problem domain — `liftoverSV`), cohort/joint genotyping stories, target-build gVCF production. These need designs of their own, not extensions of the chain-file path.

## Compatibility promises

- Reports and CLI outputs only gain fields; existing consumers keep parsing.
- `ucsc-hg19` forward conversion remains bit-for-bit the v1.0.0 behavior; the reproduction gate is re-run for releases that touch the pipeline.
- Resource cache layout changes require a migration note and a minor version.

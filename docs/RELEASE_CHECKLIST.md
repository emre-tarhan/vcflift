# Release-readiness checklist (v1.0 → v1.1.0)

## Repository identity

- [x] Canonical repository: `https://github.com/emre-tarhan/vcflift`.
- [x] Go module/import path: `github.com/emre-tarhan/vcflift`.

## Build / packaging

- [x] `go.sum` generated and committed.
- [x] one-command Linux/WSL2 native build (`build.sh`).
- [x] Windows `.exe` cross-build path from WSL2/Linux (`build.sh --windows`).
- [x] combined Linux + Windows build target (`build.sh --all`).
- [x] reusable Windows-engine artifact fetch helper (`scripts/fetch-windows-engine.sh`) + `artifacts/` auto-discovery.
- [x] Windows-engine preflight happens before lengthy dependency/test/native build work.
- [x] native Windows PowerShell helper (`build.ps1`).
- [x] clean Linux GUI build from fresh checkout.
- [x] clean WSL2 → Windows GUI cross-build with verified Windows engine artifact.
- [x] clean native Windows CI build.
- [x] Windows version metadata embedded via windres `.syso` (icon deferred; code signing deferred — unsigned builds show the standard SmartScreen prompt).
- [x] SHA256SUMS published by the release workflow; third-party notices live in `docs/THIRD_PARTY_RUNTIME.md` and the engine bundle `licenses/` directory.

## First-use / cache UX

- [x] bounded parallel reference downloads.
- [x] resumable `.part` downloads and checksums preserved.
- [x] Java + GATK first-use downloads parallelized.
- [x] reference disk-space preflight.
- [x] compressed reference archives removed after preparation by default.
- [x] cache status + safe cleanup CLI.
- [x] GUI Resources workspace with per-resource Download/Prepare states.
- [x] automatic first-run reference setup dialog when resources are missing.
- [x] embedded engine shown as read-only/included; development-engine install removed from end-user GUI.
- [x] cross-process cache/download locking (`internal/cachelock`, fail-fast advisory lock).
- [x] retry/backoff hardening for transient network failures (`internal/httpretry`, 4 attempts, exponential backoff with jitter, resumable .part reuse; 4xx/cancelled contexts fail fast).
- [x] first-run timing benchmark on representative home connection / SSD (6m18s, full reference preparation from empty cache).

## GUI

- [x] primary screen reorganized around file → inspect → convert.
- [x] engine/cache jargon removed from primary conversion workflow.
- [x] separate Resources and About workspaces.
- [x] drag-and-drop input.
- [x] explicit local/no-upload trust signals.
- [x] distinct DeepVariant vs GATK gVCF explanation.
- [x] stage-based conversion progress (no misleading long-lived 0% state) + final lifted/rejected summary.
- [x] light-only pastel/high-contrast visual direction applied after first screenshot review.
- [x] visually inspect actual Linux build and iterate spacing/min-size issues (three-tab review with user-driven iteration).
- [x] visually inspect actual Windows build and iterate platform differences (v0.9.0 smoke on real Windows: conversion + hover + console-free child processes confirmed).
- [ ] interactive cancellation/partial-output cleanup test.
- [ ] accessibility/keyboard pass.

## Real-data scientific gates

### DeepVariant `<*>` gVCF

- [x] 5,422,963 source non-reference calls conserved exactly.
- [x] 4,942,650 lifted + 480,313 rejected.
- [x] source and target REF validation.
- [x] output `<*>` / `<NON_REF>` = 0.
- [x] TBI created.
- [x] reject distributions recorded.

### Ordinary VCF

- [x] implementation + automated coverage.
- [x] real-data ordinary-VCF gate: GIAB HG002 v4.2.1 GRCh38 benchmark VCF, `variant_vcf` mode (4,048,342 in / 4,043,367 lifted / 4,975 rejected, REF validations + TBI pass) with independent GRCh37 cross-check at 98.85% exact allele concordance after normalization (see docs/VALIDATION.md).
- [x] compare ordinary-VCF result to validated gVCF candidate route: the DeepVariant-derived variant-only file carries `<*>` on variant records and re-enters the gVCF route, reproducing 5,422,963 / 4,942,650 / 480,313 exactly.

### GATK/HaplotypeCaller gVCF

- [x] source-genotyping implementation + tests.
- [x] real GATK-produced gVCF (`gatk-test-data` HG00187 reblocked exome gVCF, hg38) through pinned GenotypeGVCFs + liftover: 2,977,065 gVCF records -> 71,370 genotyped variants lifted, 479 rejected, all REF/index/placeholder QC passing.
- [x] Linux managed Java/GATK first-use smoke test (pinned Temurin JRE 17.0.20.1 + GATK 4.7.0.0 downloaded, installed and executed in the same run, 2m24s).
- [ ] Windows managed Java/GATK first-use smoke test.

## Reject research

- [x] machine-readable reject reason/contig report.
- [x] full real reject VCF audited by type and allele length.
- [x] `reject-audit` CLI command.
- [x] isolated reject rescue benchmark script + conservative parameter matrix.
- [x] run matrix on validated 480,313-record reject set locally.
- [ ] manually/independently inspect newly rescued representatives.
- [x] keep production plugin defaults after first sweep (best tested rescue 165 / 480,313 = 0.034353%).

## v1.1.0 additions

### Output contract (P0)

- [x] GUI one-sentence gVCF contract; QC `output_class: "variant_vcf"` on every conversion; `gvcf_blocks_dropped` for gVCF inputs; docs narrowed from "gVCF conversion" to variant-VCF deliverable.
- [x] 98.85% cross-check headline carries its method caveat in README/CHANGELOG.

### Target naming profiles

- [x] `--target-profile` CLI + GUI dropdown (`ucsc-hg19` default unchanged, `grch37-primary`, `hs37d5`).
- [x] grch37-primary gate: GIAB HG002 GRCh38 → GRCh37 primary naming, liftover rejects identical to baseline, 342 non-primary drops, 99.10% exact concordance with the official GRCh37 benchmark (docs/VALIDATION.md gate 4).
- [x] optional `--grch37-fasta` second REF check (pure faidx comparison; `bcftools norm -N -c e` skips the check when normalization is off — documented).

### Reverse direction hg19/GRCh37 → hg38

- [x] automatic direction detection; majority contig-length vote fixes hs37d5/GRCh37 headers (rCRS MT collides with hg38 MT length).
- [x] GRCh/b37 naming accepted via hg19 chromAlias rename map.
- [x] UCSC hg19→hg38 chain + hg19 aliases added as managed resources (checksum-verified; same UCSC license acceptance).
- [x] GATK-style hg19 gVCF rejected with guidance; DeepVariant-style gVCF via candidate extraction; profiles blocked in reverse.
- [x] reverse gate: GIAB HG002 GRCh37 benchmark, 4,033,796 in / 4,033,574 lifted / 222 rejected, zero REF mismatches both sides, 98.74% exact concordance with the official GRCh38 benchmark (docs/VALIDATION.md gate 5).
- [x] forward pipeline verified unchanged step-for-step (names, args, stage mapping) by the existing plan tests.

### Release hygiene

- [x] `Version` const → 1.1.0; Windows version metadata regenerated from it by `build.sh`.
- [x] CHANGELOG v1.1.0 section; `docs/ROADMAP.md` version discipline + shipped/next.
- [x] full non-dev build (`build.sh`, module verify + tests + vet + Linux/Windows artifacts) passing on the release machine.
- [ ] tag `v1.1.0` and push (release CI publishes with SHA256SUMS).

## v1.0 rule

Do not redesign the validated core conversion semantics without a failing correctness case. Current work should concentrate on real-data release gates, reject evidence, reproducible packaging and user experience.

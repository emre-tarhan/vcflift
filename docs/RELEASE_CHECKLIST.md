# v1.0 release-readiness checklist

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
- [ ] retry/backoff hardening for transient network failures.
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
- [ ] visually inspect actual Linux build and iterate spacing/min-size issues.
- [ ] visually inspect actual Windows build and iterate platform differences.
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

## v1.0 rule

Do not redesign the validated core conversion semantics without a failing correctness case. Current work should concentrate on real-data release gates, reject evidence, reproducible packaging and user experience.

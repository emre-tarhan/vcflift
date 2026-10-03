# Target naming profiles

The validated liftover pipeline always targets **UCSC hg19** (chr-prefixed naming, chr-prefixed header, hg19 REF validation). A target naming profile is a post-liftover layer that renames and filters the lifted output. The liftover core, its reject defaults and its REF validations are untouched.

Correct language: the `grch37-primary` and `hs37d5` profiles produce a **GRCh37 primary naming profile** of the hg19 liftover result. They do not re-run any assembly or alignment against GRCh37.

## Profiles

| Profile | Output naming | chrM records | Non-primary contigs |
| --- | --- | --- | --- |
| `ucsc-hg19` (default) | UCSC `chr1`…`chrM` | kept (NC_001807, hg19-native) | kept |
| `grch37-primary` | GRCh37 `1`–`22`, `X`, `Y` | rejected → `stale_hg19_chrM` | rejected → `non_primary_contig` |
| `hs37d5` | same as `grch37-primary` | rejected → `stale_hg19_chrM` | rejected → `non_primary_contig` |

The GRCh37 header dictionary is rebuilt from the GRCh37 primary assembly (25 contigs in sort order, MT = rCRS `16569`), and every non-contig header line of the lifted output is preserved. The output header records `##vcflift_target_profile=<profile>`.

CLI:

```bash
vcflift-cli convert --accept-ucsc-license --target-profile grch37-primary sample.hg38.vcf.gz
```

The desktop GUI exposes the same choice in the Output section (no advanced settings).

## Why chrM is rejected instead of renamed

hg19 `chrM` is the old Cambridge sequence **NC_001807** (16,571 bp). The GRCh37 mitochondrial reference is the revised Cambridge Reference Sequence, **rCRS NC_012920** (16,569 bp). The two sequences differ by insertions/substitutions, so hg19 chrM coordinates cannot be converted to GRCh37 MT coordinates by renaming. Silently relabeling chrM as MT would corrupt every downstream MT analysis, so `grch37-primary` and `hs37d5` reject those records into the profile reject bucket with the reason `stale_hg19_chrM`.

## Non-primary contigs

Records that lift to unplaced (`chrUn_*`), unlocalized (`*_random`), alternate (`*_alt`) or other non-primary hg19 contigs are not part of the GRCh37 primary assembly. They are rejected into the same bucket with the reason `non_primary_contig`.

Both profile reject classes share one sidecar, `<output>.profile-rejected.vcf.gz`, written when non-empty (kept with `--keep-rejects`, like the liftover reject VCF), and counted in the QC report under `profile_rejects`. They are separate from liftover rejects: profile-dropped records lifted successfully; they are dropped only because the requested naming profile does not carry that contig class.

## Sequence identity and REF validation

The GRCh37 primary contigs (1–22, X, Y) are sequence-identical to the UCSC hg19 primary contigs — same bases, same lengths (e.g. `2` = 243,199,373 in both; verified against the Ensembl GRCh37.p13 assembly report). The only sequence difference between the assemblies is chrM/MT. The pipeline therefore already validates every GRCh37-primary output record's REF bases through its hg19 REF validation step; a separate GRCh37 FASTA download would add no verification power and is not bundled.

For defence in depth against a wrong or mixed-up reference, `--grch37-fasta PATH` performs an optional second REF check against a user-supplied, faidx-indexed GRCh37 FASTA:

```bash
vcflift-cli convert --accept-ucsc-license --target-profile grch37-primary \
  --grch37-fasta /path/to/grch37.fa sample.hg38.vcf.gz
```

This check is a pure faidx comparison implemented in VCF Lift (`internal/profile`): it verifies every output REF base and rejects with a clear message on any mismatch. It deliberately does not use `bcftools norm`: with normalization disabled (`-N`), bcftools skips the REF check entirely, and without `-N` it would rewrite the validated representation. FASTA `N` bases match anything; records extending past a contig end or absent contigs fail the run.

## hs37d5

hs37d5 is GRCh37 plus decoy contigs (and no alts). The `hs37d5` profile delivers GRCh37 primary naming and records in its header that **decoy contigs are not produced** — decoys are not part of the GRCh37 primary assembly and a lifted output cannot reconstruct them. Use `grch37-primary` unless the hs37d5 labeling matters for your pipeline bookkeeping.

## QC and reports

- `target_profile` is recorded on every conversion (default `ucsc-hg19`).
- `profile_rejects` maps reject reasons to counts when a profile dropped records.
- Candidate conservation and gVCF block-drop math use pre-profile record counts, so profile drops never masquerade as liftover rejects.

## Validation

The `grch37-primary` profile was gated on the GIAB HG002 v4.2.1 GRCh38 benchmark through the full pipeline (see `docs/VALIDATION.md`, gate 4): 4,048,342 records in, 4,043,025 lifted into GRCh37 primary naming, liftover rejects identical to the ucsc-hg19 baseline (4,975), 342 records dropped as `non_primary_contig`. After left-aligned normalization with contig names reconciled, the profiled output matches **99.10%** of the official GRCh37 v4.2.1 benchmark exactly (3,997,316 / 4,033,796). `bcftools norm` reported zero REF mismatches on both sides against GRCh37-named hg19 primary sequence, independently confirming the sequence-identity claim behind the REF-validation coverage.

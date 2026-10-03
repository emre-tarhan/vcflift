# gVCF support design

VCF Lift treats gVCF as a first-class input type.

## What gVCF preserves that variant-only VCF does not

In a variant-only VCF, absence of a position does not by itself distinguish confidently homozygous-reference sequence from insufficient coverage, filtering, or a site that was simply not emitted.

A GATK HaplotypeCaller gVCF carries reference-confidence information across analysed territory. It uses `<NON_REF>` plus genotype likelihood/confidence fields, and commonly compresses homozygous-reference territory into blocks using `INFO/END` and `MIN_DP`.

That information is useful for callability and later genotyping, but it is still **assembly-relative** and only applies to the territory actually analysed.

## Why the default gVCF workflow changed

GATK explicitly describes HaplotypeCaller gVCF output as an intermediate file that should be processed by `GenotypeGVCFs` before final downstream analysis. `GenotypeGVCFs` accepts a single single-sample gVCF as well as combined/multi-sample inputs.

Therefore VCF Lift's recommended workflow is:

```text
hg38 GATK gVCF
 -> validate/materialize against hg38
 -> GenotypeGVCFs on hg38
 -> final ordinary hg38 VCF
 -> allele-aware hg38 -> hg19 liftover
 -> hg19 REF validation
 -> hg19 VCF
```

This preserves the source gVCF's intended statistical role before coordinate conversion.

Official references:

- https://gatk.broadinstitute.org/hc/en-us/articles/360035531812-GVCF-Genomic-Variant-Call-Format
- https://gatk.broadinstitute.org/hc/en-us/articles/360037059732-HaplotypeCaller
- https://gatk.broadinstitute.org/hc/en-us/articles/360056970432-GenotypeGVCFs


## Input `.tbi` / `.csi` sidecars

A sidecar such as `sample.gvcf.vcf.gz.tbi` is valuable. It is the random-access index for the compressed gVCF and can be reused by `GenotypeGVCFs`. VCF Lift does not trust it blindly: it checks freshness against the gVCF modification time and asks BCFtools to open the index before reuse.

This optimization is disabled when source contigs need renaming because the index describes coordinates/contig names in the original file. In that case a temporary renamed gVCF and matching temporary index are built instead. The original gVCF and its index are left untouched.

## Why not directly liftover reference blocks?

For a normal small variant, the object being transformed is a concrete allele at a genomic locus. BCFtools/liftover can transform both coordinates and alleles against the destination reference.

A reference-confidence block is different. It describes an interval `[POS, END]` and likelihoods relative to the **source reference**. Mapping only POS is insufficient. Mapping POS and END can also be insufficient if the interval crosses a chain discontinuity, assembly-specific insertion/deletion, orientation change, or target-reference difference.

BCFtools/liftover supports `--lift-end`, but that does not by itself prove that every base inside a transformed gVCF block remains statistically equivalent.

## Assembly-relative reference confidence

Suppose a sample strongly supports the hg38 reference allele at some position. If the corresponding hg19 reference base differs, that sample may become non-reference relative to hg19. The original `<NON_REF>` likelihood was not calculated as a dedicated likelihood for every possible future target-reference allele.

For this reason, coordinate transformation cannot generally reconstruct the exact gVCF that would have resulted from aligning the original reads to hg19 and calling against hg19.

## Supported modes

### Mode A — Genotype on hg38, then lift (default)

This is the recommended path for GATK-style gVCF input.

1. Detect gVCF from content.
2. Detect an adjacent `.tbi` or `.csi` index.
3. If naming already matches hg38, validate a fresh/readable existing index and reuse the original gVCF directly.
4. REF-check the source against hg38 with `bcftools norm -N -c e`, where `-N` prevents left-alignment/normalization from changing gVCF representation.
5. If the index is missing/stale/corrupt, or contig renaming is required, materialize a temporary BGZF source gVCF and build a temporary index.
6. Run GATK `GenotypeGVCFs` against hg38.
7. Validate the resulting ordinary hg38 VCF.
8. Run BCFtools/liftover.
9. Validate REF against hg19.
10. Sort, BGZF-compress, index, and report.

### Mode B — DeepVariant / `<*>` source-call extraction

For DeepVariant gVCFs this is the automatic/default dialect-specific path. DeepVariant has already finalized genotype calls and combines them with reference-confidence blocks in the gVCF. VCF Lift therefore does not run GATK `GenotypeGVCFs` on this dialect.

The extraction step uses BCFtools to keep every finalized record with a non-reference genotype (`GT="alt"`), including sequence-resolved complex records classified by BCFtools as `TYPE=OTHER`. It removes unseen `<*>`/`<NON_REF>` placeholders and trims now-unused allele-dependent fields before hg38 REF validation and liftover. No SNP/indel/MNP-only type filter is applied, because that would silently discard valid complex DeepVariant calls. `0/0` `RefCall` records and `./.` `NoCall` records are omitted because the target artifact is a variant-only hg19 VCF, not a transformed reference-confidence gVCF.

The same path can be forced with `--gvcf-mode candidate` for diagnostic or non-DeepVariant inputs, but automatic routing is preferred when the dialect can be identified confidently.

### Mode C — Preserve gVCF reference blocks (experimental / disabled)

A future implementation must map complete chain intervals, split blocks only across defensible boundaries, compare source/target reference sequence, and reject ambiguous blocks. Even then the result would be labelled a **coordinate-transformed gVCF**, not a native hg19 gVCF.

## Gold-standard native hg19 gVCF

If a downstream pipeline truly needs a gVCF that is equivalent to native calling against hg19, the strongest route remains:

```text
FASTQ / BAM / CRAM
 -> align or realign to hg19
 -> HaplotypeCaller -ERC GVCF
 -> native hg19 gVCF
```

Liftover cannot recreate read alignment/local assembly evidence that is absent from the source gVCF.

## Portable runtime policy

The recommended gVCF path is lazy-loaded. VCF Lift does not make Java/GATK a dependency for ordinary VCF users. On first recommended gVCF conversion it resolves a pinned GATK 4.7.0.0 release and a pinned Eclipse Temurin Java 17 runtime for the current platform, verifies the downloaded archives, extracts them into the application cache, validates Java major version 17, and then executes `GenotypeGVCFs`.

Environment/CLI overrides remain available for controlled installations. A user-provided Java/GATK pair must still pass the same preflight.

The current portable runtime targets Windows x64 and Linux x64, matching the v1 application platform scope.

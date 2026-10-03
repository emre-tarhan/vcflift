# Rejected variants and rescue research

VCF Lift treats a rejected variant as an explicit QC outcome rather than silently emitting a questionable target record.

## Real DeepVariant checkpoint

Validated DeepVariant 1.10.0 run (2026-10-02):

```text
liftover candidates  5,422,963
lifted               4,942,650
rejected               480,313
success               91.14298%
reject                  8.85702%
```

Reject FILTER reasons:

```text
UnmappedAnchors   479,789
MismatchAnchors       495
ApartAnchors            28
UnmappedAnchor5           1
```

The earlier ~25% reject figure from the pre-`GT="alt"` run is invalid and must not be used.

## What the real reject file tells us

`vcflift-cli reject-audit` was run against the complete 480,313-record reject VCF:

```text
SNP          456,629   95.07%
INDEL         23,653    4.92%
MIXED/OTHER       31    0.01%
```

Within `UnmappedAnchors`:

```text
SNP          456,470
INDEL         23,291
MIXED/OTHER       28
```

This indicates that the dominant reject class is **not** caused mainly by long or exotic complex alleles. Most rejected records are ordinary single-base substitutions whose anchors cannot be mapped consistently through the selected chain under current plugin semantics.

Allele-length profile of the reject records is also strongly short-variant dominated:

```text
max allele length = 1        456,629 records
max allele length = 2         17,511
max allele length = 3-5        4,025
max allele length > 1000           5
maximum observed length         2,038 bp
```

## Built-in audit command

```bash
vcflift-cli reject-audit sample.hg19.vcf.gz.rejected.vcf.gz
```

The JSON includes total records, FILTER distribution, SNP/INDEL/MIXED type distribution, FILTER×type matrix, contigs and allele-length bins.

## Rescue benchmark

The project includes:

```bash
scripts/reject-rescue-benchmark.sh \
  --input sample.hg19.vcf.gz.rejected.vcf.gz
```

It operates only on the existing reject VCF, so parameter research does not need to rerun the full 5.4M-call input every time.

Matrix currently tested:

```text
baseline        max-snp-gap=1   max-indel-inc=250
snp_gap_2       max-snp-gap=2   max-indel-inc=250
snp_gap_5       max-snp-gap=5   max-indel-inc=250
snp_gap_10      max-snp-gap=10  max-indel-inc=250
indel_inc_500   max-snp-gap=1   max-indel-inc=500
indel_inc_1000  max-snp-gap=1   max-indel-inc=1000
combined_5_500  max-snp-gap=5   max-indel-inc=500
```

The plugin defaults are `max-snp-gap=1` and `max-indel-inc=250`. The baseline rerun is a determinism sanity check: if the same reject set unexpectedly lifts under unchanged defaults, the benchmark must be investigated before interpreting any rescue results.

### 2026-10-02 real benchmark result

The complete validated 480,313-record reject set was benchmarked. Every case conserved the input count and every rescued output passed target hg19 REF validation:

| case | max-snp-gap | max-indel-inc | rescued | still rejected | rescued % |
|---|---:|---:|---:|---:|---:|
| baseline | 1 | 250 | 0 | 480,313 | 0.000000% |
| snp_gap_2 | 2 | 250 | 0 | 480,313 | 0.000000% |
| snp_gap_5 | 5 | 250 | 50 | 480,263 | 0.010410% |
| snp_gap_10 | 10 | 250 | 165 | 480,148 | 0.034353% |
| indel_inc_500 | 1 | 500 | 9 | 480,304 | 0.001874% |
| indel_inc_1000 | 1 | 1000 | 17 | 480,296 | 0.003539% |
| combined_5_500 | 5 | 500 | 59 | 480,254 | 0.012284% |

The strongest tested setting rescues only **165 / 480,313 (0.034353%)** records. This is not enough benefit to justify relaxing production defaults, especially before exact source/target allele review on multiple callsets. Production remains at the plugin defaults.

For every case the script now:

1. verifies the reject VCF still matches hg38 REF;
2. reruns allele-aware liftover with `--write-reject`, `--write-fail` and `--write-src`;
3. validates all candidate rescued records against hg19 REF with `bcftools norm -f ... -c e`;
4. verifies `lifted + rejected = benchmark input records`;
5. writes `results.tsv`, a fresh reject-audit JSON and a small `*.rescued.tsv` containing target coordinates/alleles plus source annotations for exact review.

## Production decision rule

A larger rescue count is **not sufficient** to change defaults. Any candidate setting must also demonstrate:

- stable target coordinate + REF/ALT representation;
- correct strand and allele transforms;
- preserved genotype/allele indexing;
- target REF validation;
- no disproportionate new failures in another variant class;
- reproducibility on more than one real callset.

No coordinate-only UCSC `liftOver` fallback will be used to override an allele-aware rejection.

## Next reject milestone

The first parameter sweep is complete and does **not** support a production-default change. The next research step is narrower: inspect the source-aware `snp_gap_10.rescued.tsv` / `combined_5_500.rescued.tsv` records, then test any promising behavior on an independent callset and/or alternative trusted chain. Reject rescue is therefore research work, not a v1.0 release blocker.

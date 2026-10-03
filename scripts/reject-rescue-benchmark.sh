#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI="$ROOT/dist/vcflift-cli"
CACHE="${VCFLIFT_CACHE:-$HOME/.cache/vcflift}"
INPUT=""
OUTDIR=""

usage() {
  cat <<'USAGE'
Benchmark bcftools/liftover rescue parameters on an existing VCF Lift reject VCF.
This is a research/development tool; it never changes production defaults.

Usage:
  scripts/reject-rescue-benchmark.sh --input sample.hg19.vcf.gz.rejected.vcf.gz [options]

Options:
  --cache DIR     VCF Lift cache root (default: ~/.cache/vcflift)
  --out DIR       Benchmark output directory (default: ./reject-benchmark-<timestamp>)
  -h, --help      Show help.

The benchmark reruns only the rejected hg38 records, not the full source callset.
It checks source REF, runs a conservative parameter matrix, validates every rescued
record against hg19 REF, and writes TSV + per-case reject files for audit.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --input) shift; INPUT="${1:-}" ;;
    --cache) shift; CACHE="${1:-}" ;;
    --out) shift; OUTDIR="${1:-}" ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

[[ -n "$INPUT" && -f "$INPUT" ]] || { echo "error: --input rejected.vcf.gz is required" >&2; exit 1; }
[[ -x "$CLI" ]] || { echo "error: build the CLI first: ./build.sh --cli-only" >&2; exit 1; }
[[ -n "$OUTDIR" ]] || OUTDIR="$PWD/reject-benchmark-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUTDIR"

ENGINE_JSON="$OUTDIR/engine.json"
"$CLI" engine status --cache "$CACHE" > "$ENGINE_JSON"
readarray -t ENGINE < <(python3 - "$ENGINE_JSON" <<'PY'
import json,sys
j=json.load(open(sys.argv[1]))
i=j['installation']
print(i['bcftools'])
print(i['plugin_dir'])
PY
)
BCF="${ENGINE[0]}"
PLUGIN_DIR="${ENGINE[1]}"
HG38="$CACHE/resources/v1/hg38.fa"
HG19="$CACHE/resources/v1/hg19.fa"
CHAIN="$CACHE/resources/v1/hg38ToHg19.over.chain.gz"
for p in "$BCF" "$HG38" "$HG19" "$CHAIN"; do
  [[ -e "$p" ]] || { echo "error: required file missing: $p" >&2; exit 1; }
done
export BCFTOOLS_PLUGINS="$PLUGIN_DIR"

TOTAL=$("$BCF" view -H "$INPUT" | wc -l | tr -d ' ')
echo "Input rejects: $TOTAL"
echo "Validating that reject records still match hg38 REF..."
"$BCF" norm -f "$HG38" -c e -Ou "$INPUT" >/dev/null

printf 'case\tmax_snp_gap\tmax_indel_inc\tlifted\trejected\trescued_pct\tconserved\ttarget_ref_valid\n' > "$OUTDIR/results.tsv"

run_case() {
  local label="$1" snp="$2" indel="$3"
  local out="$OUTDIR/$label.hg19.vcf.gz"
  local rej="$OUTDIR/$label.rejected.vcf.gz"
  echo "==> $label  --max-snp-gap=$snp --max-indel-inc=$indel"

  "$BCF" +liftover -Ou "$INPUT" -- \
    -s "$HG38" -f "$HG19" -c "$CHAIN" \
    --max-snp-gap "$snp" --max-indel-inc "$indel" \
    --reject "$rej" --reject-type z --write-reject --write-fail --write-src \
    | "$BCF" norm -f "$HG19" -c e -Oz -o "$out"

  local lifted rejected conserved valid
  lifted=$("$BCF" view -H "$out" | wc -l | tr -d ' ')
  rejected=$("$BCF" view -H "$rej" | wc -l | tr -d ' ')
  if [[ $((lifted + rejected)) -eq "$TOTAL" ]]; then conserved=yes; else conserved=no; fi
  if "$BCF" norm -f "$HG19" -c e -Ou "$out" >/dev/null 2>&1; then valid=yes; else valid=no; fi
  local pct
  pct=$(awk -v n="$lifted" -v total="$TOTAL" 'BEGIN { if (total>0) printf "%.6f", 100*n/total; else printf "0.000000" }')
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$label" "$snp" "$indel" "$lifted" "$rejected" "$pct" "$conserved" "$valid" >> "$OUTDIR/results.tsv"
  "$BCF" query -f '%CHROM\t%POS\t%REF\t%ALT\t%FILTER\t%INFO\n' "$out" > "$OUTDIR/$label.rescued.tsv"
  "$CLI" reject-audit "$rej" > "$OUTDIR/$label.reject-audit.json"
}

# Plugin defaults are 1 / 250. The baseline is a determinism sanity check:
# rerunning the reject set with defaults should not magically rescue records.
run_case baseline 1 250
run_case snp_gap_2 2 250
run_case snp_gap_5 5 250
run_case snp_gap_10 10 250
run_case indel_inc_500 1 500
run_case indel_inc_1000 1 1000
run_case combined_5_500 5 500

echo
echo "Benchmark complete: $OUTDIR/results.tsv"
echo "Do not adopt a non-default setting based on rescue count alone; review coordinate/allele stability and reject-reason shifts first."

#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <engine-dir>" >&2
  exit 2
fi

ENGINE_DIR="$(cd "$1" && pwd)"
if [ -x "$ENGINE_DIR/bin/bcftools.exe" ]; then
  BCFTOOLS="$ENGINE_DIR/bin/bcftools.exe"
else
  BCFTOOLS="$ENGINE_DIR/bin/bcftools"
fi
PLUGIN_DIR="$ENGINE_DIR/plugins"

[ -x "$BCFTOOLS" ] || { echo "bcftools executable not found under $ENGINE_DIR/bin" >&2; exit 1; }
[ -d "$PLUGIN_DIR" ] || { echo "plugin directory not found: $PLUGIN_DIR" >&2; exit 1; }

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t vcflift-smoke)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

# 100bp single-contig identity references. Keeping source and target identical
# makes this smoke test independent of chain orientation while still exercising
# FASTA lookup, chain parsing, plugin loading, variant transformation and output.
SEQ="ACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGT"
printf '>chr1\n%s\n' "$SEQ" > "$TMP/src.fa"
printf '>chr1\n%s\n' "$SEQ" > "$TMP/dst.fa"
printf 'chr1\t100\t6\t100\t101\n' > "$TMP/src.fa.fai"
printf 'chr1\t100\t6\t100\t101\n' > "$TMP/dst.fa.fai"

cat > "$TMP/identity.chain" <<'CHAIN'
chain 1000 chr1 100 + 0 100 chr1 100 + 0 100 1
100
CHAIN

cat > "$TMP/input.vcf" <<'VCF'
##fileformat=VCFv4.2
##contig=<ID=chr1,length=100>
#CHROM	POS	ID	REF	ALT	QUAL	FILTER	INFO
chr1	2	.	C	T	60	PASS	.
VCF

if [[ "$BCFTOOLS" == *.exe ]] && command -v cygpath >/dev/null 2>&1; then
  export PATH="$ENGINE_DIR/bin:$PATH"
  export BCFTOOLS_PLUGINS="$(cygpath -m "$PLUGIN_DIR")"
else
  export BCFTOOLS_PLUGINS="$PLUGIN_DIR"
fi

"$BCFTOOLS" plugin -l | grep -Fx liftover >/dev/null
"$BCFTOOLS" +liftover -Ou "$TMP/input.vcf" -- \
  -s "$TMP/src.fa" \
  -f "$TMP/dst.fa" \
  -c "$TMP/identity.chain" \
  --reject "$TMP/reject.vcf" \
  --write-src \
  --write-reject \
  | "$BCFTOOLS" sort -Ov -o "$TMP/output.vcf"

awk -F '\t' '$1=="chr1" && $2==2 && $4=="C" && $5=="T" {ok=1} END{exit !ok}' "$TMP/output.vcf"
echo "VCF Lift engine smoke test passed: $($BCFTOOLS --version | head -n 1)"

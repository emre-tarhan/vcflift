#!/usr/bin/env bash
set -euo pipefail

BCFTOOLS_VERSION="${BCFTOOLS_VERSION:-1.24}"
SCORE_REF="${SCORE_REF:-master}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="${VCFLIFT_ENGINE_WORK:-$ROOT/.engine-build/linux-amd64}"
OUT="${VCFLIFT_ENGINE_OUT:-$ROOT/build/engine/linux-amd64}"
JOBS="${JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)}"

rm -rf "$WORK" "$OUT"
mkdir -p "$WORK" "$OUT/bin" "$OUT/plugins" "$OUT/licenses"
cd "$WORK"

curl -fL --retry 3 -o "bcftools-${BCFTOOLS_VERSION}.tar.bz2" \
  "https://github.com/samtools/bcftools/releases/download/${BCFTOOLS_VERSION}/bcftools-${BCFTOOLS_VERSION}.tar.bz2"
tar -xjf "bcftools-${BCFTOOLS_VERSION}.tar.bz2"

git clone --filter=blob:none https://github.com/freeseek/score.git score
git -C score checkout "$SCORE_REF"
SCORE_COMMIT="$(git -C score rev-parse HEAD)"

BCF="bcftools-${BCFTOOLS_VERSION}"
cp score/liftover.c score/score.h "$BCF/plugins/"

cd "$BCF"
# Release tarballs bundle HTSlib and generated files. Build only what VCF Lift needs;
# GSL/Perl filters are deliberately not enabled.
make -j"$JOBS" bcftools plugins/liftover.so

cp bcftools "$OUT/bin/bcftools"
cp plugins/liftover.so "$OUT/plugins/liftover.so"
cp LICENSE "$OUT/licenses/bcftools-LICENSE.txt"
cp ../score/LICENSE "$OUT/licenses/score-LICENSE.txt"
chmod 0755 "$OUT/bin/bcftools"

BCFTOOLS_PLUGINS="$OUT/plugins" "$OUT/bin/bcftools" plugin -l | grep -Fx liftover >/dev/null
HELP="$(BCFTOOLS_PLUGINS="$OUT/plugins" "$OUT/bin/bcftools" +liftover -h 2>&1 || true)"
for required in --write-src --write-reject --lift-end; do
  printf '%s\n' "$HELP" | grep -F -- "$required" >/dev/null || {
    echo "liftover plugin missing required option: $required" >&2
    exit 1
  }
done

bash "$ROOT/scripts/smoke-test-engine.sh" "$OUT"

cd "$ROOT"
go run ./tools/engine-manifest \
  --root "$OUT" \
  --platform linux-amd64 \
  --engine-version "bcftools-${BCFTOOLS_VERSION}-score-${SCORE_COMMIT:0:12}" \
  --bcftools bin/bcftools \
  --plugin-dir plugins \
  --bcftools-version "bcftools ${BCFTOOLS_VERSION}" \
  --score-ref "$SCORE_COMMIT"

printf 'Built %s\n' "$OUT"
ldd "$OUT/bin/bcftools" || true

#!/usr/bin/env bash
set -euo pipefail

BCFTOOLS_VERSION="${BCFTOOLS_VERSION:-1.24}"
SCORE_REF="${SCORE_REF:-master}"
if [[ -n "${GITHUB_WORKSPACE:-}" ]]; then
  ROOT="$(cygpath -u "$GITHUB_WORKSPACE")"
else
  ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fi

MINGW_PREFIX="${MINGW_PREFIX:-/ucrt64}"
WORK="${VCFLIFT_ENGINE_WORK:-$ROOT/.engine-build/windows-amd64}"
OUT="${VCFLIFT_ENGINE_OUT:-$ROOT/build/engine/windows-amd64}"
JOBS="${JOBS:-${NUMBER_OF_PROCESSORS:-2}}"

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

if [[ "${MSYSTEM:-}" != "UCRT64" ]]; then
  echo "error: Windows engine must be built inside an MSYS2 UCRT64 shell; MSYSTEM=${MSYSTEM:-unset}" >&2
  exit 1
fi

export PATH="$MINGW_PREFIX/bin:$PATH"

# BCFtools/HTSlib require the configure path on Windows/MSYS2.
# This performs the platform feature checks required by HTSlib and
# selects the Windows DLL plugin format.
./configure

make -j"$JOBS" \
  bcftools \
  plugins/liftover.dll

cp bcftools.exe "$OUT/bin/bcftools.exe"
cp plugins/liftover.dll "$OUT/plugins/liftover.dll"
cp LICENSE "$OUT/licenses/bcftools-LICENSE.txt"
cp ../score/LICENSE "$OUT/licenses/score-LICENSE.txt"

HTS_DLL="$(find htslib-* -maxdepth 1 -type f \
  \( -name 'hts-*.dll' -o -name 'libhts*.dll' \) \
  -print -quit)"

if [[ -z "$HTS_DLL" ]]; then
  echo "error: HTSlib runtime DLL was not produced by the Windows build" >&2
  exit 1
fi

cp "$HTS_DLL" "$OUT/bin/"

HTS_DIR="$(dirname "$HTS_DLL")"

# MinGW builds depend on a set of runtime DLLs. Copy only DLLs reported by ldd
# from the MINGW64 prefix so the bundle can run from an ordinary Windows process.
OLD_PATH="$PATH"

# Let ldd resolve the freshly-built HTSlib DLL while collecting
# all UCRT runtime dependencies.
export PATH="$HTS_DIR:$MINGW_PREFIX/bin:$OLD_PATH"

{
  ldd bcftools.exe || true
  ldd plugins/liftover.dll || true
  ldd "$HTS_DLL" || true
} | awk -v p="$MINGW_PREFIX/" '
  $3 ~ "^" p { print $3 }
  $1 ~ "^" p { print $1 }
' | sort -u | while read -r dll; do
  [[ -f "$dll" ]] && cp -n "$dll" "$OUT/bin/"
done

export PATH="$OUT/bin:$MINGW_PREFIX/bin:$OLD_PATH"

PLUGIN_DIR_NATIVE="$(cygpath -m "$OUT/plugins")"

echo "==> Verifying packaged Windows plugin"

BCFTOOLS_PLUGINS="$PLUGIN_DIR_NATIVE" \
  "$OUT/bin/bcftools.exe" plugin --list-plugins --verbosity 2

BCFTOOLS_PLUGINS="$PLUGIN_DIR_NATIVE" \
  "$OUT/bin/bcftools.exe" plugin -l \
  | grep -Fx liftover >/dev/null

BCFTOOLS_PLUGINS="$OUT/plugins" "$OUT/bin/bcftools.exe" plugin -l | grep -Fx liftover >/dev/null
HELP="$(
  BCFTOOLS_PLUGINS="$PLUGIN_DIR_NATIVE" \
    "$OUT/bin/bcftools.exe" +liftover -h 2>&1 || true
)"
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
  --platform windows-amd64 \
  --engine-version "bcftools-${BCFTOOLS_VERSION}-score-${SCORE_COMMIT:0:12}" \
  --bcftools bin/bcftools.exe \
  --plugin-dir plugins \
  --bcftools-version "bcftools ${BCFTOOLS_VERSION}" \
  --score-ref "$SCORE_COMMIT"

printf 'Built %s\n' "$OUT"

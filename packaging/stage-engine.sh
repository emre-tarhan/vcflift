#!/usr/bin/env bash
set -euo pipefail
if [ "$#" -ne 2 ]; then
  echo "usage: $0 <engine-artifact-dir> <platform>" >&2
  exit 2
fi
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$1"
PLATFORM="$2"
DST="$ROOT/internal/enginebundle/payload/$PLATFORM"
[ -f "$SRC/manifest.json" ] || { echo "missing $SRC/manifest.json" >&2; exit 1; }
rm -rf "$DST"
mkdir -p "$DST"
cp -R "$SRC"/. "$DST"/
echo "staged $PLATFORM engine payload"

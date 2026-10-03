#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="${VCFLIFT_REPO:-emre-tarhan/vcflift}"
ARTIFACT_NAME="engine-windows-amd64"
DEST="$ROOT/artifacts/vcflift-engine-windows-amd64.zip"

command -v gh >/dev/null 2>&1 || {
  echo "error: GitHub CLI (gh) is required to fetch the CI artifact." >&2
  echo "Install it, authenticate with 'gh auth login', then rerun this command." >&2
  exit 1
}
command -v zip >/dev/null 2>&1 || {
  echo "error: zip is required. On Ubuntu/WSL: sudo apt-get install -y zip" >&2
  exit 1
}

run_id="$(
  gh api "/repos/$REPO/actions/artifacts?per_page=100" \
    --jq "[
      .artifacts[]
      | select(
          .name == \"$ARTIFACT_NAME\"
          and .expired == false
        )
    ]
    | sort_by(.created_at)
    | reverse
    | .[0].workflow_run.id // empty"
)"

if [[ -z "$run_id" ]]; then
  cat >&2 <<EOF2
error: no live '$ARTIFACT_NAME' artifact was found for $REPO.

Push an engine-build change (Native engine builds now runs automatically),
or run it manually once, then rerun:

  ./scripts/fetch-windows-engine.sh
EOF2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "==> Downloading $ARTIFACT_NAME from GitHub Actions run $run_id"
gh run download "$run_id" \
  --repo "$REPO" \
  --name "$ARTIFACT_NAME" \
  --dir "$tmp"

manifest="$(find "$tmp" -type f -name manifest.json -print -quit)"
if [[ -z "$manifest" ]]; then
  echo "error: downloaded artifact does not contain manifest.json" >&2
  exit 1
fi
engine_root="$(dirname "$manifest")"

mkdir -p "$(dirname "$DEST")"
rm -f "$DEST"
(
  cd "$engine_root"
  zip -qr "$DEST" .
)

echo "Saved verified Windows engine artifact input to:"
echo "  $DEST"
echo
echo "Next:"
echo "  ./build.sh --all --install-deps --clean"

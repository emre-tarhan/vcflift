#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
find "$ROOT/internal/enginebundle/payload" -mindepth 1 -maxdepth 1 -type d -exec rm -rf {} +

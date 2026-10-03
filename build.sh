#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

INSTALL_DEPS=0
CLI_ONLY=0
GUI_ONLY=0
DEV=0
SKIP_TESTS=0
CLEAN=0
WINDOWS=0
ALL=0
WINDOWS_ENGINE=""
WINDOWS_ENGINE_RESOLVED=""

usage() {
  cat <<'USAGE'
VCF Lift developer build helper

Usage:
  ./build.sh [options]

Targets:
  (default)            Build native Linux/WSL2 CLI + GUI.
  --windows            Build Windows x64 .exe files from Linux/WSL2.
  --all                Build native Linux and Windows x64 artifacts in one run.
  --cli-only           Build only CLI artifacts for the selected target(s).
  --gui                Build only GUI artifacts for the selected target(s).

Modes:
  --dev                Fast development build: skips go mod download/verify,
                       go test and go vet. The Windows engine payload is
                       restaged only when its content actually changes.
                       Use this for day-to-day iteration; do not combine
                       with --clean unless the tree feels broken.

Setup/options:
  --install-deps       Install required Debian/Ubuntu build dependencies.
  --windows-engine P   Stage a verified Windows engine directory or .zip.
                       Optional when the bundle already exists at
                       artifacts/vcflift-engine-windows-amd64.zip or has
                       already been staged in the source tree.
  --skip-tests         Skip core go test/go vet checks (implied by --dev).
  --clean              Remove generated build/dist directories before building.
                       For release-style fresh builds; not needed for normal
                       development.
  -h, --help           Show this help.

Fast UI iteration (Linux):
  ./build.sh --dev --gui

Fast Windows GUI cross-build from WSL2 (engine fetched once):
  ./build.sh --dev --gui --windows

Recommended first native build on Ubuntu/WSL2:
  ./build.sh --install-deps --clean

Portable Windows build from WSL2 after fetching the engine once:
  ./scripts/fetch-windows-engine.sh
  ./build.sh --windows --install-deps

Build Linux + Windows artifacts together:
  ./build.sh --all --install-deps --clean
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-deps) INSTALL_DEPS=1 ;;
    --cli-only) CLI_ONLY=1 ;;
    --gui) GUI_ONLY=1 ;;
    --dev) DEV=1; SKIP_TESTS=1 ;;
    --skip-tests) SKIP_TESTS=1 ;;
    --clean) CLEAN=1 ;;
    --windows) WINDOWS=1 ;;
    --all) ALL=1 ;;
    --windows-engine)
      shift
      [[ $# -gt 0 ]] || { echo "error: --windows-engine requires a path" >&2; exit 2; }
      WINDOWS_ENGINE="$1"
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if [[ "$WINDOWS" -eq 1 && "$ALL" -eq 1 ]]; then
  echo "error: choose either --windows or --all, not both" >&2
  exit 2
fi

if [[ "$CLI_ONLY" -eq 1 && "$GUI_ONLY" -eq 1 ]]; then
  echo "error: choose either --cli-only or --gui, not both" >&2
  exit 2
fi

# ---- timing helpers (bash >= 5 provides EPOCHREALTIME) ----
now_us() { echo "${EPOCHREALTIME/./}"; }
STEP_LABEL=""
STEP_T=0
BUILD_T=0
step_begin() { STEP_LABEL="$1"; STEP_T=$(now_us); echo "==> $1"; }
step_end() {
  local e=$(( $(now_us) - STEP_T ))
  printf '    done in %d.%01ds\n' $((e / 1000000)) $(((e / 100000) % 10))
}

BUILD_T=$(now_us)

resolve_windows_engine_input() {
  local requested="$1"
  local candidates=()
  if [[ -n "$requested" ]]; then
    candidates+=("$requested")
  else
    candidates+=(
      "$ROOT/artifacts/vcflift-engine-windows-amd64.zip"
      "$ROOT/vcflift-engine-windows-amd64.zip"
      "$ROOT/artifacts/windows-engine-amd64"
    )
  fi

  local candidate
  for candidate in "${candidates[@]}"; do
    if [[ -e "$candidate" ]]; then
      WINDOWS_ENGINE_RESOLVED="$candidate"
      return 0
    fi
  done

  if [[ -f "$ROOT/internal/enginebundle/payload/windows-amd64/manifest.json" ]]; then
    WINDOWS_ENGINE_RESOLVED=""
    return 0
  fi

  if [[ -n "$requested" ]]; then
    echo "error: Windows engine path not found: $requested" >&2
  else
    echo "error: no verified Windows engine bundle is available." >&2
  fi
  cat >&2 <<'EOF'

A portable Windows build needs the Windows bcftools/liftover engine once.
After this repository is pushed to GitHub, run the "Native engine builds"
workflow, then fetch its artifact with:

  ./scripts/fetch-windows-engine.sh

The helper stores it at:
  artifacts/vcflift-engine-windows-amd64.zip

Then rerun simply:
  ./build.sh --all --install-deps --clean

You can also pass an existing verified directory or zip explicitly with
--windows-engine PATH. The Go .exe can be cross-compiled in WSL; only the
native Windows engine payload must first come from the Windows/MSYS2 CI job.
EOF
  exit 1
}

if [[ "$WINDOWS" -eq 1 || "$ALL" -eq 1 ]]; then
  # Fail before installing dependencies/tests/native builds instead of waiting
  # until the final Windows stage.
  resolve_windows_engine_input "$WINDOWS_ENGINE"
fi

if [[ "$CLEAN" -eq 1 ]]; then
  echo "==> Cleaning generated directories"
  rm -rf dist build .engine-build
fi

if ! command -v go >/dev/null 2>&1; then
  echo "error: Go is required (project target: Go 1.23.x)." >&2
  exit 1
fi

echo "==> Go: $(go version)"

if [[ "$INSTALL_DEPS" -eq 1 ]]; then
  if ! command -v apt-get >/dev/null 2>&1; then
    echo "error: --install-deps currently supports Debian/Ubuntu apt only." >&2
    echo "See docs/DEVELOPMENT.md for other platforms." >&2
    exit 1
  fi
  sudo apt-get update
  packages=(unzip zip)
  if [[ "$WINDOWS" -eq 1 || "$ALL" -eq 1 ]]; then
    packages+=(gcc-mingw-w64-x86-64)
  fi
  if [[ "$WINDOWS" -eq 0 && "$CLI_ONLY" -eq 0 ]]; then
    packages+=(gcc pkg-config libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev)
  fi
  step_begin "Installing build dependencies"
  sudo apt-get install -y "${packages[@]}"
  step_end
fi

# go.sum is part of a reproducible source release. Older development archives
# omitted it, so bootstrap it once whenever a GUI/cross build needs modules.
if [[ "$DEV" -eq 1 && -f go.sum ]]; then
  echo "==> Dev mode: skipping module download/verify (go build fetches as needed)"
elif [[ "$CLI_ONLY" -eq 0 || "$WINDOWS" -eq 1 || "$ALL" -eq 1 ]]; then
  if [[ ! -f go.sum ]]; then
    step_begin "go.sum is missing; resolving pinned Go modules once with 'go mod tidy'"
    go mod tidy
    echo "==> go.sum created. Keep/commit it."
    step_end
  else
    step_begin "Downloading/verifying Go modules"
    go mod download
    step_end
  fi
  step_begin "Verifying module cache integrity"
  go mod verify
  step_end
elif [[ -f go.sum ]]; then
  echo "==> Verifying existing Go module checksums"
  go mod verify
else
  echo "==> go.sum not present; continuing because native --cli-only does not import Fyne"
fi

if [[ "$DEV" -eq 1 ]]; then
  echo "==> Dev mode: skipping go test/go vet"
elif [[ "$SKIP_TESTS" -eq 0 ]]; then
  step_begin "Running core tests"
  go test ./internal/... ./cmd/vcflift-cli
  step_end
  step_begin "Running core vet"
  go vet ./internal/... ./cmd/vcflift-cli
  step_end
fi

mkdir -p dist

# Fingerprint an engine source (zip file or directory) by content only, so
# mtimes never matter. The state file lives in build/ and is reset by --clean.
engine_source_fingerprint() {
  local src="$1"
  if [[ -f "$src" ]]; then
    sha256sum "$src" | awk '{print $1}'
  else
    (cd "$src" && find . -type f -print0 | sort -z | xargs -0 sha256sum) | sha256sum | awk '{print $1}'
  fi
}

stage_windows_engine() {
  local src="$1"
  [[ -e "$src" ]] || { echo "error: Windows engine path not found: $src" >&2; exit 1; }
  local stage_src="$src"
  if [[ -f "$src" ]]; then
    case "${src,,}" in
      *.zip) ;;
      *) echo "error: --windows-engine must be a directory or .zip" >&2; exit 1 ;;
    esac
  fi

  local state_file="$ROOT/build/engine-stage.state"
  local fp
  fp=$(engine_source_fingerprint "$src")

  if [[ -f "$state_file" && "$(cat "$state_file" 2>/dev/null)" == "$fp" \
        && -f "$ROOT/internal/enginebundle/payload/windows-amd64/manifest.json" ]]; then
    echo "==> Windows engine payload already staged (content unchanged, hash ${fp:0:12}); skipping restage"
    return 0
  fi

  step_begin "Staging Windows native engine from $src"
  if [[ -f "$src" ]]; then
    stage_src="$ROOT/build/windows-engine-input"
    rm -rf "$stage_src"
    mkdir -p "$stage_src"
    unzip -q "$src" -d "$stage_src"
  fi
  [[ -f "$stage_src/manifest.json" ]] || {
    echo "error: Windows engine artifact must contain manifest.json at its root" >&2
    exit 1
  }
  ./packaging/stage-engine.sh "$stage_src" windows-amd64
  mkdir -p "$ROOT/build"
  printf '%s\n' "$fp" > "$state_file"
  step_end
}

check_linux_gui_deps() {
  [[ "$CLI_ONLY" -eq 1 ]] && return 0
  [[ "$(go env GOOS)" != "linux" ]] && return 0
  local missing=()
  local pc
  for pc in gl x11 xcursor xrandr xi xinerama xxf86vm wayland-client xkbcommon; do
    if ! command -v pkg-config >/dev/null 2>&1 || ! pkg-config --exists "$pc"; then
      missing+=("$pc")
    fi
  done
  if [[ ${#missing[@]} -gt 0 ]]; then
    echo "error: Linux GUI development packages are missing (${missing[*]})." >&2
    echo "Rerun once with: ./build.sh --install-deps" >&2
    exit 1
  fi
}

build_native() {
  if [[ "$GUI_ONLY" -eq 0 ]]; then
    step_begin "Building native CLI"
    go build -trimpath -ldflags="-s -w" -o dist/vcflift-cli ./cmd/vcflift-cli
    step_end
  fi
  if [[ "$CLI_ONLY" -eq 1 ]]; then
    return
  fi
  check_linux_gui_deps
  step_begin "Building native GUI"
  go build -trimpath -ldflags="-s -w" -o dist/VCFLift ./cmd/vcflift
  step_end
}

# Windows version metadata: render a .syso resource from the Version constant
# so the .exe reports FileVersion/ProductVersion in Explorer. Optional; the
# build proceeds without it when windres is not installed.
generate_windows_syso() {
  local version
  version=$(grep -oE 'Version = "[^"]+"' internal/converter/native.go | head -1 | sed 's/.*"\(.*\)"/\1/')
  local numeric
  numeric=$(printf '%s' "$version" | grep -oE '^[0-9]+\.[0-9]+\.[0-9]+')
  if [[ -z "$numeric" ]]; then
    echo "warning: could not parse version '$version' for Windows metadata; skipping .syso" >&2
    return 0
  fi
  local a b c
  IFS='.' read -r a b c <<< "$numeric"
  mkdir -p build
  sed -e "s/{FILE_VERSION_NUM}/$a,$b,$c,0/g" \
      -e "s/{FILE_VERSION}/$version/g" \
      packaging/windows-version.rc.in > build/windows-version.rc
  x86_64-w64-mingw32-windres -O coff \
    -o cmd/vcflift/vcflift_windows_amd64.syso build/windows-version.rc
}

check_windows_gui_metadata() {
  if command -v x86_64-w64-mingw32-windres >/dev/null 2>&1; then
    generate_windows_syso
  else
    echo "warning: x86_64-w64-mingw32-windres missing; building without Windows version metadata" >&2
  fi
}

build_windows() {
  [[ "$(go env GOOS)" == "linux" ]] || echo "warning: WSL/Linux is the supported cross-build host for this helper" >&2
  if [[ -n "$WINDOWS_ENGINE_RESOLVED" ]]; then
    stage_windows_engine "$WINDOWS_ENGINE_RESOLVED"
  fi
  if [[ ! -f internal/enginebundle/payload/windows-amd64/manifest.json ]]; then
    echo "error: portable Windows build requires internal/enginebundle/payload/windows-amd64." >&2
    echo "Provide a verified bundle with --windows-engine <vcflift-engine-windows-amd64.zip>." >&2
    echo "The embedded Linux bcftools/liftover payload cannot run inside a Windows .exe." >&2
    exit 1
  fi

  if [[ "$GUI_ONLY" -eq 0 ]]; then
    step_begin "Building Windows CLI"
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
      go build -trimpath -ldflags="-s -w" -o dist/vcflift-cli-windows-amd64.exe ./cmd/vcflift-cli
    step_end
  fi

  if [[ "$CLI_ONLY" -eq 1 ]]; then
    return
  fi
  if ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
    echo "error: x86_64-w64-mingw32-gcc is missing. Rerun with --install-deps." >&2
    exit 1
  fi
  check_windows_gui_metadata
  step_begin "Building Windows GUI"
  CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
    go build -trimpath -ldflags="-s -w -H windowsgui" -o dist/VCFLift-windows-amd64.exe ./cmd/vcflift
  step_end
}

if [[ "$WINDOWS" -eq 1 ]]; then
  build_windows
elif [[ "$ALL" -eq 1 ]]; then
  build_native
  build_windows
else
  build_native
fi

e=$(( $(now_us) - BUILD_T ))
echo
echo "Build complete in $((e / 1000000)).$(((e / 100000) % 10))s. Artifacts in $ROOT/dist:"
find "$ROOT/dist" -maxdepth 1 -type f -printf '  %f\n' | sort

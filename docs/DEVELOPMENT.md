# Development and build guide

Canonical repository: `https://github.com/emre-tarhan/vcflift`  
Go module path: `github.com/emre-tarhan/vcflift`

## Supported release targets

VCF Lift targets Linux amd64 and Windows amd64 for both GUI and CLI.

## Linux / WSL2 native build

First build:

```bash
./build.sh --install-deps --clean
```

Later builds:

```bash
./build.sh
```

Fast development iteration:

```bash
./build.sh --dev --gui
```

CLI only:

```bash
./build.sh --cli-only
```

GUI only:

```bash
./build.sh --gui
```

A full build resolves the pinned Fyne dependency and creates `go.sum` if an older development source archive omitted it. `go.sum` is a source/release artifact and must be kept once generated.

## Fast development builds vs release builds

`--dev` is the day-to-day mode. It skips `go mod download`/`go mod verify`, `go test` and `go vet`, and restages the Windows engine payload only when the source artifact content actually changes (SHA-256 fingerprint stored in `build/engine-stage.state`, reset by `--clean`). The default (non-`--dev`) mode still runs module verification and core tests/vet, and is the right choice before pushing or validating a release.

Key rules:

- `--clean` removes only `dist/`, `build/` and `.engine-build/`; it never touches the Go build cache. Use it for fresh release-style builds, not for normal development.
- Never run `go clean -cache` during development; warm-cache rebuilds take seconds.
- GUI UI iteration on Linux: `./build.sh --dev --gui`.
- Windows GUI cross iteration: `./build.sh --dev --gui --windows` (requires the engine zip in `artifacts/` once, or an already-staged payload).
- Engine payload staging is content-hashed: unchanged `artifacts/vcflift-engine-windows-amd64.zip` means zero restage work, and Go's `//go:embed` recompiles nothing because the content is identical.

Measured on the reference WSL2 machine (Go 1.23.0, 2026-10-03):

| Scenario | Cold build cache | Warm cache (`--dev`) |
|---|---:|---:|
| Linux CLI | 17.1s | ~1.0s |
| Linux GUI | 463.2s (~7.7 min) | ~0.5s |
| Windows CLI cross | 27.6s | ~0.4s |
| Windows GUI cross | 665.7s (~11.1 min) | ~0.4s |
| `./build.sh --dev --gui` total | — | 2.2s |
| `./build.sh --dev --gui --windows` total | — | 1.3s |

Cold numbers are a one-time cost per machine/toolchain (fresh `GOCACHE`), dominated by compiling Fyne and its C dependencies. Overheads that `--dev` removes, measured on the same machine: `go mod verify` 4.2s warm and up to 65s when the OS page cache is cold (it re-reads the entire ~2.5 GB module cache), warm `go test` 1.3s rising to ~50s after Go cache invalidation, and the unconditional engine re-extract performed by earlier `build.sh` versions on every Windows build.

When Go cache entries are missing (first build of the day after toolchain or dependency changes), a Windows GUI cross-build can still take a few minutes; that is inherent to compiling Fyne for a second platform and is why daily UI work should use the Linux GUI while Windows smoke tests run in CI.

## Cross-compile Windows from WSL2/Linux

A Windows `.exe` can be produced from Linux. Two separate requirements matter:

1. **Fyne GUI:** requires a Windows C cross-compiler because the desktop driver uses CGO. `build.sh --windows --install-deps` installs `gcc-mingw-w64-x86-64` on Debian/Ubuntu.
2. **Native engine:** the executable must embed the Windows bcftools/liftover payload. A Linux bcftools binary cannot be used inside a Windows executable.

The native Windows engine is built on the Windows/MSYS2 GitHub Actions job, while the Go/Fyne `.exe` can still be built from WSL/Linux. After the repository is on GitHub, run **Native engine builds** once and fetch its artifact:

```bash
./scripts/fetch-windows-engine.sh
```

This writes `artifacts/vcflift-engine-windows-amd64.zip`. `build.sh` auto-detects that path, so the normal Windows cross-build becomes:

```bash
./build.sh --windows --install-deps
```

This produces:

```text
dist/VCFLift-windows-amd64.exe
dist/vcflift-cli-windows-amd64.exe
```

To build Linux and Windows artifacts together in one WSL/Linux run:

```bash
./build.sh --all --install-deps --clean
```

The engine bundle can come from the `engine-windows-amd64` CI artifact or a native Windows/MSYS2 engine build. The app itself can then be cross-compiled repeatedly from WSL2 using that same verified engine payload. `--windows-engine PATH` remains available for explicit overrides. Missing Windows-engine input is checked before dependency installation/tests/builds, so `--all` no longer wastes a full Linux build before reporting the problem.

A CLI-only Windows PE can be cross-compiled without Fyne/CGO, but **do not distribute it as a self-contained VCF Lift release unless a Windows engine payload is embedded or separately supplied**.

## Native Windows build

PowerShell helper:

```powershell
.\build.ps1 -Clean -EngineBundle .\vcflift-engine-windows-amd64.zip
```

A full Fyne build requires a Windows GCC (for example MSYS2 MinGW64 `gcc`) in `PATH`.

## Engine release builds

Release CI builds bcftools 1.24 plus the pinned `freeseek/score` liftover plugin separately for Linux and Windows, stages the corresponding payload, and then compiles each application target.

Important files:

```text
scripts/build-engine-linux.sh
scripts/build-engine-windows-msys2.sh
packaging/stage-engine.sh
.github/workflows/release.yml
```

## Tests

Core checks:

```bash
go test ./internal/... ./cmd/vcflift-cli
go vet ./internal/... ./cmd/vcflift-cli
```

A GUI build additionally requires Fyne dependencies and platform graphics toolchains.

## Reject research tools

Audit a reject VCF:

```bash
./dist/vcflift-cli reject-audit sample.rejected.vcf.gz
```

Benchmark only rejected records with alternative plugin parameters:

```bash
scripts/reject-rescue-benchmark.sh \
  --input sample.hg19.vcf.gz.rejected.vcf.gz
```

This script is research-only. Production defaults must not be changed solely because a parameter produces a larger rescue count. The validated first sweep rescued at most 165 / 480,313 records (0.034353%), so production remains at the plugin defaults. Current benchmark output also includes source-aware `*.rescued.tsv` files for exact coordinate/allele review.

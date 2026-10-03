# Native engine bundles

VCF Lift treats BCFtools/liftover as a versioned native engine rather than an arbitrary PATH dependency.

## Supported engine

The v1 conversion profile is pinned to **BCFtools 1.24** plus a compatible `freeseek/score` `liftover` plugin. Preflight requires the plugin options used by VCF Lift, including `--write-src`, `--write-reject`, and `--lift-end`.

Engine resolution order during conversion is:

1. an explicit override (`--bcftools` / `--plugin-dir` or environment override),
2. the engine embedded in an official release build,
3. a verified engine bundle installed in the VCF Lift cache,
4. a compatible BCFtools installation found on PATH (development/advanced fallback).

This keeps normal releases reproducible even when a different system BCFtools happens to be installed.

## Import an existing BCFtools 1.24 build

On the same platform as the binary:

```bash
vcflift-cli engine import \
  --bcftools /path/to/bcftools \
  --plugin-dir /path/to/plugins \
  --score-ref <freeseek-score-commit-if-known> \
  --bundle-out vcflift-engine-linux-amd64.zip
```

Windows example:

```powershell
vcflift-cli.exe engine import `
  --bcftools C:\tools\bcftools\bcftools.exe `
  --plugin-dir C:\tools\bcftools\plugins `
  --score-ref local-unverified `
  --bundle-out vcflift-engine-windows-amd64.zip
```

The importer:

- executes BCFtools and loads the liftover plugin as a preflight,
- runs a real synthetic identity-chain liftover smoke test before accepting the engine,
- requires BCFtools 1.24,
- fingerprints every bundled file with SHA-256,
- writes a content-addressed engine version into `manifest.json`,
- installs the same verified bundle into the local VCF Lift cache for immediate testing.

On Windows it reads the PE import tables for `bcftools.exe`, `liftover.so`, and discovered MinGW DLLs. Non-system DLL dependencies are followed recursively. Windows system DLLs are never copied. If a dependency is available outside PATH, add its directory with `--runtime-dirs`.

## Install a bundle

```bash
vcflift-cli engine install --bundle vcflift-engine-windows-amd64.zip
vcflift-cli engine status
```

The desktop GUI also has Engine bundle import for development setups is handled by build tooling and `vcflift-cli engine` commands; the desktop GUI exposes no engine-install control.

## Embed a verified bundle into a custom build

Run this on the target platform:

```bash
go run ./tools/engine-stage \
  --bundle vcflift-engine-linux-amd64.zip
```

This verifies the bundle and stages it under:

```text
internal/enginebundle/payload/<goos>-<goarch>/
```

A subsequent GUI/CLI build embeds that payload using Go `embed`.

## Bundle layout

```text
manifest.json
bin/
  bcftools[.exe]
  <runtime DLLs where required>
plugins/
  liftover.so
licenses/
  ...
```

`manifest.json` records the engine format, platform, BCFtools version string, plugin source reference when known, and SHA-256 for every payload file.

## Release artifacts

Release CI builds BCFtools from source, pins the plugin source revision, smoke-tests a real liftover using a synthetic VCF/reference/identity chain, embeds the engine into the desktop and CLI binaries, and additionally publishes standalone `vcflift-engine-<platform>.zip` bundles for diagnostics/development use.

param(
    [switch]$Clean,
    [switch]$CliOnly,
    [switch]$SkipTests,
    [string]$EngineBundle = ""
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

if ($Clean) {
    Write-Host "==> Cleaning generated directories"
    Remove-Item -Recurse -Force dist, build, .engine-build -ErrorAction SilentlyContinue
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go is required (project target: Go 1.23.x)."
}
Write-Host "==> $(go version)"

if ($EngineBundle) {
    $stage = Join-Path $Root "build\windows-engine-input"
    Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force $stage | Out-Null
    if ((Get-Item $EngineBundle).PSIsContainer) {
        Copy-Item "$EngineBundle\*" $stage -Recurse
    } else {
        Expand-Archive -Path $EngineBundle -DestinationPath $stage -Force
    }
    if (-not (Test-Path (Join-Path $stage "manifest.json"))) {
        throw "Windows engine bundle must contain manifest.json at its root."
    }
    $payload = Join-Path $Root "internal\enginebundle\payload\windows-amd64"
    Remove-Item -Recurse -Force $payload -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force $payload | Out-Null
    Copy-Item "$stage\*" $payload -Recurse
    Write-Host "==> Staged windows-amd64 engine payload"
}

if (-not $CliOnly) {
    if (-not (Test-Path go.sum)) {
        Write-Host "==> Creating missing go.sum"
        go mod tidy
    } else {
        go mod download
    }
    go mod verify
}

if (-not $SkipTests) {
    go test ./internal/... ./cmd/vcflift-cli
    go vet ./internal/... ./cmd/vcflift-cli
}

New-Item -ItemType Directory -Force dist | Out-Null
Write-Host "==> Building Windows CLI"
go build -trimpath -ldflags="-s -w" -o dist\vcflift-cli-windows-amd64.exe ./cmd/vcflift-cli

if (-not $CliOnly) {
    if (-not (Test-Path internal\enginebundle\payload\windows-amd64\manifest.json)) {
        throw "Portable GUI build requires a staged windows-amd64 engine. Pass -EngineBundle <zip>."
    }
    if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
        throw "Fyne GUI build needs a Windows C compiler (for example MSYS2 MinGW64 gcc) in PATH."
    }
    Write-Host "==> Building Windows GUI"
    go build -trimpath -ldflags="-s -w -H windowsgui" -o dist\VCFLift-windows-amd64.exe ./cmd/vcflift
}

Write-Host "Build complete: $Root\dist"

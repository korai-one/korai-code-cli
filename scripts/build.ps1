#!/usr/bin/env pwsh
# Build the korai CLI to bin/korai.exe on Windows.
#
# The binary embeds tree-sitter grammars via cgo, so a C compiler (gcc) must be
# on PATH and CGO must be enabled. This script ensures both, then builds.
#
# Usage: pwsh scripts/build.ps1
$ErrorActionPreference = 'Stop'

# Repo root is the parent of this script's directory, so the build works from
# any current directory.
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# cgo needs a C compiler. If gcc isn't on PATH, fall back to the WinLibs
# (mingw-w64) toolchain installed under the winget packages directory.
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    $winget = Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages'
    $gcc = Get-ChildItem -Path $winget -Recurse -Filter gcc.exe -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -match 'mingw64\\bin\\gcc.exe$' } |
        Select-Object -First 1
    if ($gcc) {
        $env:Path = "$($gcc.DirectoryName);$env:Path"
    } else {
        Write-Error "no gcc found: install a C compiler (e.g. winget install -e --id BrechtSanders.WinLibs.POSIX.UCRT)"
    }
}

$env:CGO_ENABLED = '1'

Write-Host "building bin/korai.exe (gcc: $((Get-Command gcc).Source))"
go build -o bin/korai.exe ./cmd/korai
Write-Host "built bin/korai.exe"

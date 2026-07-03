#!/usr/bin/env bash
# Build the korai CLI to bin/korai on Linux/macOS.
#
# The binary embeds tree-sitter grammars via cgo, so a C compiler must be
# available and CGO must be enabled. This script ensures both, then builds.
#
# Usage: scripts/build.sh
set -euo pipefail

# Repo root is the parent of this script's directory, so the build works from
# any current directory.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

# cgo needs a C compiler. Honor an explicit $CC, else look for cc/gcc/clang.
if [ -z "${CC:-}" ] && ! command -v cc >/dev/null 2>&1 \
    && ! command -v gcc >/dev/null 2>&1 && ! command -v clang >/dev/null 2>&1; then
    echo "no C compiler found: install one (e.g. 'xcode-select --install' on macOS, or gcc/clang via your package manager)" >&2
    exit 1
fi

export CGO_ENABLED=1

echo "building bin/korai (cc: ${CC:-$(command -v cc || command -v gcc || command -v clang)})"
go build -o bin/korai ./cmd/korai
echo "built bin/korai"

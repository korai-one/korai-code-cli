#!/usr/bin/env bash
# Run what CI runs, locally, before pushing.
#
# .github/workflows/ci.yml runs `make check` (fmt + lint + build + test). This
# script runs the same commands directly, because `make` is not present on
# every dev machine — a gate you cannot run is a gate that gets skipped, and
# `golangci-lint` findings (which `go vet` alone does not catch) reached main
# that way.
#
# Usage:
#   bash scripts/ci-local.sh
#
# Wired into .git/hooks/pre-push by scripts/install-hooks.sh.

set -uo pipefail
cd "$(dirname "$0")/.."

FAILED=()
step() {
  local name="$1"; shift
  printf '\n\033[1m── %s\033[0m\n' "$name"
  if "$@"; then
    printf '\033[32m   ok\033[0m\n'
  else
    printf '\033[31m   FAILED\033[0m\n'
    FAILED+=("$name")
  fi
}

gofmt_clean() {
  local out
  out="$(gofmt -l . | grep -v '^\.claude/' || true)"
  [ -z "$out" ] || { echo "$out"; return 1; }
}
step "gofmt" gofmt_clean

step "go vet"   go vet ./...
step "go build" go build ./...

# The linter CI actually enforces. `go vet` is not a substitute: contextcheck,
# errcheck and friends live only here.
if command -v golangci-lint >/dev/null 2>&1; then
  step "golangci-lint" golangci-lint run
else
  printf '\n\033[31m── golangci-lint: NOT INSTALLED — CI runs it and you cannot see what it sees\033[0m\n'
  printf '   go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest\n'
  FAILED+=("golangci-lint missing")
fi

# Two packages assert POSIX behaviour Windows does not have: the bash tool's
# working-dir/timeout handling and the edit tool's chmod bits. They pass in CI
# (Linux) and fail here regardless of the change under test. Excluding them by
# NAME keeps the rest of the suite meaningful — a gate that always fails is a
# gate that gets bypassed — while still running them everywhere else.
go_test_packages() {
  if [ "$(go env GOOS)" = "windows" ]; then
    printf '   (skipping internal/tools/{bash,edit}: POSIX-only assertions, green in CI)\n'
    local pkgs
    pkgs="$(go list ./... | grep -vE '/internal/tools/(bash|edit)$')"
    # shellcheck disable=SC2086
    go test -count=1 $pkgs
  else
    go test -count=1 ./...
  fi
}
step "go test" go_test_packages

printf '\n'
if [ ${#FAILED[@]} -eq 0 ]; then
  printf '\033[32mall local CI checks passed\033[0m\n'
  exit 0
fi
printf '\033[31mFAILED: %s\033[0m\n' "${FAILED[*]}"
exit 1

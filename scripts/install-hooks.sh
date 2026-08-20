#!/usr/bin/env bash
# Install the pre-push hook that runs CI's checks locally.
#
# Hooks live in .git/hooks, which git does not track, so a fresh clone has
# none — hence this script rather than a committed hook file. Idempotent: run
# it again to repair or update the hook.
#
#   bash scripts/install-hooks.sh
#
# Bypass for a genuine emergency (a docs-only hotfix while CI is wedged):
#   git push --no-verify

set -euo pipefail
cd "$(dirname "$0")/.."

HOOK=".git/hooks/pre-push"
mkdir -p .git/hooks

cat > "$HOOK" <<'HOOK_EOF'
#!/usr/bin/env bash
# Runs CI's fast checks before a push. Installed by scripts/install-hooks.sh.
# Bypass with `git push --no-verify` only when you have a reason.
set -uo pipefail
repo_root="$(git rev-parse --show-toplevel)"
printf '\033[1mpre-push: running local CI checks (QUICK=1 to skip the race detector)\033[0m\n'
if ! bash "$repo_root/scripts/ci-local.sh"; then
  printf '\n\033[31mpush aborted: fix the above, or `git push --no-verify` to override\033[0m\n'
  exit 1
fi
HOOK_EOF

chmod +x "$HOOK"
echo "installed $HOOK"

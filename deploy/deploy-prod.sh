#!/usr/bin/env bash
# Atomic host-native deploy of the aphrollo dev-env CLI (/usr/local/bin/aphrollo).
#
# Invoked by .github/workflows/pipeline.yml's `deploy` job on the self-hosted
# runner, on push-to-main (post-merge) and manual workflow_dispatch. Stages a
# release dir, smoke-tests the new binary, then atomically flips the `current`
# symlink. No daemon to restart — every coder/devops/operator session execs
# /usr/local/bin/aphrollo fresh per call, so a swapped `current` is picked up on
# the next exec. That also means NO sudo: the deploy only writes under
# /opt/aphrollo-cli (runner-writable); /usr/local/bin/aphrollo is a root-made
# symlink → /opt/aphrollo-cli/current/aphrollo (see deploy/README.md).
#
# Server prerequisites (one-time, provisioned by aphrollo-infra — see
# deploy/README.md):
#   - /opt/aphrollo-cli              github-runner-writable (atomic current swap)
#   - /opt/aphrollo-cli/releases     github-runner-writable (staged releases)
#   - /usr/local/bin/aphrollo        symlink → /opt/aphrollo-cli/current/aphrollo
#
# Rollback is implicit: the new binary is smoke-tested BEFORE the swap, so a
# broken build never becomes `current` — the last good release keeps serving.

set -euo pipefail

SHA="${GITHUB_SHA:-$(git rev-parse HEAD)}"
SHA_SHORT="${SHA:0:7}"
TS=$(date +%Y%m%d-%H%M%S)
RELEASE_NAME="${TS}-${SHA_SHORT}"

OPT_BASE=/opt/aphrollo-cli
RELEASES="${OPT_BASE}/releases"
CURRENT="${OPT_BASE}/current"
TARGET="${RELEASES}/${RELEASE_NAME}"

say()  { printf '\033[36m▸ %s\033[0m\n' "$*"; }
ok()   { printf '\033[32m✓ %s\033[0m\n' "$*"; }
fail() { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

[ -f bin/aphrollo ] || fail "missing bin/aphrollo — did go build run?"
[ -d "$RELEASES" ]  || fail "$RELEASES missing — run the aphrollo-infra prereqs (deploy/README.md)"

say "stage release ${RELEASE_NAME}"
mkdir -p "$TARGET"
install -m 755 bin/aphrollo "$TARGET/aphrollo"
echo "$SHA" > "$TARGET/SHA"

# Smoke the staged binary BEFORE swapping it in — a non-runnable build (bad
# arch, missing subcommand wiring) must not replace a working `current`. These
# are the same zero-exit invocations the PR `build` job uses.
say "smoke ${RELEASE_NAME}"
"$TARGET/aphrollo" tdd --help >/dev/null || fail "smoke failed: aphrollo tdd --help"
"$TARGET/aphrollo" --help >/dev/null     || fail "smoke failed: aphrollo --help"

say "atomic swap current → ${RELEASE_NAME}"
PREV_TARGET=$(readlink -f "$CURRENT" 2>/dev/null || true)
ln -sfn "$TARGET" "${CURRENT}.new"
mv -Tf "${CURRENT}.new" "$CURRENT"
ok "aphrollo now ${RELEASE_NAME} ($([ -n "$PREV_TARGET" ] && basename "$PREV_TARGET" || echo 'first release') → ${RELEASE_NAME})"

say "prune old releases (keep last 5)"
# Skip directories the runner cannot delete (e.g. bootstrap/ provisioned by root).
# `-writable` is a GNU find extension that tests whether the current user can
# write the directory entry; non-writable dirs (different owner, read-only) are
# silently left alone.
find "$RELEASES" -maxdepth 1 -mindepth 1 -type d -writable -printf '%T@ %p\0' \
  | sort -rnz \
  | cut -d' ' -f2- -z \
  | tail -n +6 -z \
  | xargs -0 -r rm -rf

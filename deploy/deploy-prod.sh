#!/usr/bin/env bash
# Atomic host-native deploy of the aphrollo dev-env CLI (/usr/local/bin/aphrollo).
#
# Invoked by .github/workflows/pipeline.yml's `deploy` job on the self-hosted
# runner, on push-to-main (post-merge) and manual workflow_dispatch, in a
# checkout of the newest release tag. Stages a
# release dir, smoke-tests the new binary, then atomically flips the `current`
# symlink. No daemon to restart — every coder/devops/operator session execs
# /usr/local/bin/aphrollo fresh per call, so a swapped `current` is picked up on
# the next exec. /usr/local/bin/aphrollo is a root-made symlink →
# /opt/aphrollo-cli/current/aphrollo (see deploy/README.md). /opt/aphrollo-cli is
# root-owned and the binary is the one every session runs, so the runner never
# writes it: it stages the build and the root-owned installer does the rest.
#
# Two paths, one build:
#   - installer (the host's normal path): the runner stages the build in
#     /var/lib/aphrollo-release-staging/aphrollo-cli and calls
#     `sudo aphrollo-install-release aphrollo-cli`, which verifies it, installs a
#     root-owned release, smoke-tests the new binary as an unprivileged user
#     BEFORE the swap, swaps `current` and prunes. /opt/aphrollo-cli is
#     root-owned, so the runner writes none of it.
#   - in-script (below the installer block): used only while the host has not yet
#     applied the aphrollo-infra change that provides the installer. Delete it
#     once every host has.
#
# Server prerequisites (one-time, provisioned by aphrollo-infra — see
# deploy/README.md):
#   - /opt/aphrollo-cli              root-owned (aphrollo-infra)
#   - /opt/aphrollo-cli/releases     root-owned (aphrollo-infra)
#   - github-runner sudoers rule for `aphrollo-install-release aphrollo-cli`
#   - /usr/local/bin/aphrollo        symlink → /opt/aphrollo-cli/current/aphrollo
#
# Rollback is implicit: the new binary is smoke-tested BEFORE the swap, so a
# broken build never becomes `current` — the last good release keeps serving.

set -euo pipefail

# The deploy follows the newest release TAG (deploy/newest-tag.sh), never the
# tip of main: the workflow checks the tag out before building, and this
# refuses to ship a checkout that is not exactly it.
TAG=$(bash "$(dirname "$0")/newest-tag.sh")
TAG_SHA=$(git rev-parse "${TAG}^{commit}")
SHA="${GITHUB_SHA:-$(git rev-parse HEAD)}"
[ "$SHA" = "$TAG_SHA" ] || { echo "refusing to deploy ${SHA:0:7}: the newest release tag $TAG is ${TAG_SHA:0:7}" >&2; exit 1; }
SHA_SHORT="${SHA:0:7}"
TS=$(date +%Y%m%d-%H%M%S)
RELEASE_NAME="${TS}-${TAG}-${SHA_SHORT}"

OPT_BASE=/opt/aphrollo-cli
RELEASES="${OPT_BASE}/releases"
CURRENT="${OPT_BASE}/current"
TARGET="${RELEASES}/${RELEASE_NAME}"

say()  { printf '\033[36m▸ %s\033[0m\n' "$*"; }
ok()   { printf '\033[32m✓ %s\033[0m\n' "$*"; }
fail() { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

[ -f bin/aphrollo ] || fail "missing bin/aphrollo — did go build run?"

# stage_artifact <dir>: lay the release out in <dir> (the staging dir for the
# installer, the release dir for the in-script path).
stage_artifact() {
	local dest=$1
	install -m 755 bin/aphrollo "$dest/aphrollo"
	echo "$SHA" > "$dest/SHA"
}

# --- installer path --------------------------------------------------------------
INSTALLER="${APHROLLO_INSTALLER:-/usr/local/bin/aphrollo-install-release}"
STAGING="${APHROLLO_RELEASE_STAGING:-/var/lib/aphrollo-release-staging}/aphrollo-cli"
if [ -x "$INSTALLER" ] && sudo -n -l "$INSTALLER" aphrollo-cli >/dev/null 2>&1; then
	[ -d "$STAGING" ] || fail "$STAGING missing — apply aphrollo-infra (deploy-infra.yml)"
	say "stage release ${RELEASE_NAME} in ${STAGING}"
	find "$STAGING" -mindepth 1 -delete
	stage_artifact "$STAGING"
	say "manifest"
	(cd "$STAGING" && find . -type f ! -name MANIFEST.sha256 -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > MANIFEST.sha256)
	say "install via ${INSTALLER}"
	sudo -n "$INSTALLER" aphrollo-cli
	ok "aphrollo-cli installed from ${STAGING}"
	exit 0
fi

# --- in-script path (host without the installer) ------------------------------------
[ -d "$RELEASES" ] || fail "$RELEASES missing — run the aphrollo-infra prereqs (deploy/README.md)"

say "stage release ${RELEASE_NAME}"
mkdir -p "$TARGET"
stage_artifact "$TARGET"

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

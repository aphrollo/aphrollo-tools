#!/usr/bin/env bash
# Tag the commit that carries a new internal/buildinfo/VERSION as
# v<VERSION> and push the tag. A version already tagged, here or on origin,
# is [skip]: a merge that does not bump VERSION adds no tag, and a rerun of
# the same merge does nothing. Run by the release job on a push to main, in a
# checkout that has fetched every tag.
#
# The tag goes on $GITHUB_SHA (HEAD when unset). Set NO_PUSH=1 to tag locally
# only.
set -euo pipefail

version=$(tr -d '[:space:]' < internal/buildinfo/VERSION)
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "internal/buildinfo/VERSION is \"$version\", not MAJOR.MINOR.PATCH" >&2
  exit 1
fi
tag="v$version"

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  echo "[skip] $tag already exists"
  exit 0
fi
if [ -z "${NO_PUSH:-}" ] && git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
  echo "[skip] $tag already exists on origin"
  exit 0
fi

git tag "$tag" "${GITHUB_SHA:-HEAD}"
if [ -z "${NO_PUSH:-}" ]; then
  git push origin "refs/tags/$tag"
fi
echo "tagged $tag at $(git rev-parse --short "$tag^{commit}")"

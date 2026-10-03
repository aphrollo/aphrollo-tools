#!/usr/bin/env bash
# Make the release tag a push to main owes, and the GitHub Release behind the
# newest tag. Run by the pipeline's release job on a push to main, in a checkout
# that has fetched every tag, after `go build -o bin/aphrollo ./cmd/aphrollo`.
#
# The decision is not made here. `aphrollo release plan` reads the changelog.d
# fragments at the commit that the newest v* tag does not contain and prints the
# tag they earn (the newest bumped by the highest level among them), or nothing.
# This script only does what it says: tag that commit, push the tag, and make
# sure the newest tag has a Release whose notes are the fragments it was the
# first to contain (`aphrollo changelog --tag`). No commit is made to main.
#
# Idempotent: a tag that exists, here or on origin, is [skip], and a rerun after
# a Release failed gives the newest tag the Release it lacks. Two runs never mint
# the same tag: the plan is relative to the newest tag, and the workflow runs
# one release at a time.
#
# The tag goes on $GITHUB_SHA (HEAD when unset). $APHROLLO is the binary
# (./bin/aphrollo). NO_PUSH=1 tags locally only and NO_RELEASE=1 skips the
# Release; the tests use both.
set -euo pipefail

aphrollo=${APHROLLO:-./bin/aphrollo}
sha=${GITHUB_SHA:-HEAD}

tag=$("$aphrollo" release plan --rev "$sha")
if [ -z "$tag" ]; then
  echo "[skip] no release: no new changelog fragment of level patch, minor or major"
else
  if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "aphrollo release plan printed \"$tag\", not v<MAJOR.MINOR.PATCH>" >&2
    exit 1
  fi
  if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
    echo "[skip] $tag already exists"
  elif [ -z "${NO_PUSH:-}" ] && git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
    echo "[skip] $tag already exists on origin"
  else
    git tag "$tag" "$sha"
    if [ -z "${NO_PUSH:-}" ]; then
      git push origin "refs/tags/$tag"
    fi
    echo "tagged $tag at $(git rev-parse --short "$tag^{commit}")"
  fi
fi

if [ -n "${NO_RELEASE:-}" ]; then
  exit 0
fi
newest=$(bash "$(dirname "$0")/newest-tag.sh")
if gh release view "$newest" >/dev/null 2>&1; then
  echo "[skip] release $newest already exists"
  exit 0
fi
notes=$(mktemp)
trap 'rm -f "$notes"' EXIT
"$aphrollo" changelog --tag "$newest" --rev "$newest" > "$notes"
gh release create "$newest" --verify-tag --title "$newest" --notes-file "$notes"
echo "released $newest"

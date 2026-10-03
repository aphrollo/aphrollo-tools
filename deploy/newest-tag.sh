#!/usr/bin/env bash
# Print the newest release tag (v<MAJOR.MINOR.PATCH>) of the checkout it runs
# in, highest semantic version first; exit 1 with a message when there is none.
# `aphrollo update` and the deploy both follow this tag, not the tip of main.
set -euo pipefail

tag=$(git tag --list 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true)
if [ -z "$tag" ]; then
  echo "no release tag (v<MAJOR.MINOR.PATCH>) in this checkout; fetch tags first" >&2
  exit 1
fi
echo "$tag"

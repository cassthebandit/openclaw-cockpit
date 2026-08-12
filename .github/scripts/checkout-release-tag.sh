#!/usr/bin/env bash
# Validates and checks out a manually dispatched release tag. The tag arrives
# via the RELEASE_TAG environment variable — never interpolated into shell
# source — so a hostile input cannot become shell syntax. Only an exact
# existing vMAJOR.MINOR.PATCH tag is accepted.
set -euo pipefail

tag="${RELEASE_TAG:-}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'invalid release tag: %q (want vMAJOR.MINOR.PATCH)\n' "$tag" >&2
  exit 1
fi
if ! git rev-parse --verify --quiet "refs/tags/${tag}^{commit}" >/dev/null; then
  echo "release tag does not exist: ${tag}" >&2
  exit 1
fi
git checkout --detach "refs/tags/${tag}"

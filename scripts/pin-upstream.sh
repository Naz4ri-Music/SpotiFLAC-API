#!/usr/bin/env bash
set -euo pipefail

REPO="https://github.com/afkarxyz/SpotiFLAC.git"
API_URL="https://api.github.com/repos/afkarxyz/SpotiFLAC/commits"

build_pseudo_from_sha() {
  local sha="$1"
  local iso
  iso=$(curl -fsSL "${API_URL}/${sha}" | sed -n 's/.*"date": "\([0-9-]*T[0-9:]*\)Z".*/\1/p' | head -n1)
  if [ -z "$iso" ]; then
    echo "Could not retrieve commit date for ${sha}" >&2
    exit 1
  fi

  local ts
  ts=$(date -u -j -f "%Y-%m-%dT%H:%M:%S" "$iso" "+%Y%m%d%H%M%S" 2>/dev/null || date -u -d "$iso" "+%Y%m%d%H%M%S")
  if [ -z "$ts" ]; then
    echo "Could not parse commit date ${iso}" >&2
    exit 1
  fi

  echo "v0.0.0-${ts}-${sha:0:12}"
}

if [ "$#" -gt 1 ]; then
  echo "Usage: $0 [pseudo-version|latest]"
  echo "Example: $0 latest"
  echo "Example: $0 v0.0.0-20260212123831-1314c14c592f"
  exit 1
fi

VERSION=""

if [ "$#" -eq 0 ] || [ "$1" = "latest" ]; then
  SHA=$(git ls-remote "$REPO" HEAD | awk '{print $1}')
  if [ -z "$SHA" ]; then
    echo "Could not resolve HEAD for ${REPO}" >&2
    exit 1
  fi
  VERSION=$(build_pseudo_from_sha "$SHA")
else
  VERSION="$1"
fi

go mod edit -replace="spotiflac=github.com/afkarxyz/SpotiFLAC@${VERSION}"
go mod tidy

echo "Upstream pinned to ${VERSION}"

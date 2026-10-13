#!/usr/bin/env bash
# Prints the CHANGELOG.md section of a version (without its heading and link
# references). Fails when the section is missing or empty.
#   scripts/changelog-section.sh 0.1.0-alpha.1
set -euo pipefail
version="${1:?usage: changelog-section.sh <version>}"
section="$(awk -v v="$version" '
  /^## \[/ { if (found) exit; found = index($0, "[" v "]") > 0; next }
  found && !/^\[[^]]+\]: /
' "$(dirname "$0")/../CHANGELOG.md")"
if ! grep -q '[^[:space:]]' <<<"$section"; then
  echo "No section [$version] in CHANGELOG.md" >&2
  exit 1
fi
printf '%s\n' "$section"

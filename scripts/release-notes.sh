#!/bin/sh
# Print the release notes for a version: its CHANGELOG.md section, with
# the screenshot linked absolutely (release pages do not resolve
# repository paths).
#
#   scripts/release-notes.sh 0.1.0 [OWNER/REPO]
#
# OWNER/REPO defaults to $GITHUB_REPOSITORY (set in GitHub Actions).
set -eu
cd "$(dirname "$0")/.."
ver=${1#v}
repo=${2:-${GITHUB_REPOSITORY:?give OWNER/REPO or set GITHUB_REPOSITORY}}
awk -v h="## [$ver]" '
  index($0, h) == 1 { on = 1; next }
  on && /^## \[/ { exit }
  on { print }
' CHANGELOG.md |
  sed "s#](docs/#](https://github.com/$repo/raw/HEAD/docs/#g" |
  sed '/./,$!d' > "${TMPDIR:-/tmp}/notes.$$"
if [ ! -s "${TMPDIR:-/tmp}/notes.$$" ]; then
  echo "no CHANGELOG.md section for $ver" >&2
  rm -f "${TMPDIR:-/tmp}/notes.$$"
  exit 1
fi
cat "${TMPDIR:-/tmp}/notes.$$"
rm -f "${TMPDIR:-/tmp}/notes.$$"

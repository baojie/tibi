#!/bin/bash
# The 0.0.15 picture: the Chrome extension's settings and popup, taken by
# the extension's browser test (scripts/e2e/extension.mjs, SHOT_DIR) after
# it has uploaded an image, a text selection, a screenshot and a big file.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo"
work=$(mktemp -d "${TMPDIR:-/tmp}/tibi-shots.XXXXXX")
trap 'rm -rf "$work"' EXIT
go build -o "$work/tibi" ./cmd/tibi
SHOT_DIR=$work node scripts/e2e/extension.mjs "$work/tibi" "${CHROME:-google-chrome}"
card() { # card IN OUT: white card with a soft shadow
  magick "$1" -bordercolor '#d0d5dd' -border 1 \( +clone -background '#00000055' -shadow 60x10+0+6 \) \
    +swap -background none -layers merge +repage "$2"
}
card "$work/ext-options.png" "$work/a.png"
card "$work/ext-popup.png" "$work/b.png"
magick -size 1020x470 xc:'#e9ecf1' "$work/a.png" -geometry +20+20 -composite \
  "$work/b.png" -geometry +612+70 -composite docs/screenshots/v0.0.15.png
echo "wrote docs/screenshots/v0.0.15.png"

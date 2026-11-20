#!/bin/bash
# The 0.0.17 picture: uploading from ACDSeeN (a separate project; give its
# checkout, set up with its own ./setup.sh, as ACDSEEN=…, default
# ../acdseen next to this repository).
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo"
acd=$(cd "${ACDSEEN:-$repo/../acdseen}" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/tibi-shots.XXXXXX")
pid=
trap '[ -n "$pid" ] && kill $pid 2>/dev/null; rm -rf "$work"' EXIT

go build -o "$work/tibi" ./cmd/tibi
export TIBI_DATA_DIR="$work/data" TIBI_LISTEN=127.0.0.1:18111 TIBI_BASE_URL=http://localhost:18111
TIBI_TOKEN=$("$work/tibi" admin token create --name desktop --scopes upload --config "" 2>/dev/null)
"$work/tibi" serve --config "" 2>"$work/serve.log" &
pid=$!
export TIBI_SERVER=$TIBI_BASE_URL TIBI_TOKEN XDG_CONFIG_HOME=$work/config HOME=$work
for _ in $(seq 20); do curl -sf "$TIBI_SERVER/healthz" >/dev/null && break; sleep 0.3; done

# A folder of holiday pictures.
mkdir -p "$work/Pictures/Holiday"
i=0
for c in '#87c5ea #f6d6a8 #2f6b4f' '#2b1055 #f6a05b #1d1238' '#0d1b3e #5a4a8a #141a33' \
         '#f9c784 #fcefd4 #5b7553' '#a8dadc #f1faee #1d3557' '#ffcdb2 #ffb4a2 #6d6875'; do
  set -- $c
  i=$((i + 1))
  magick -size 800x600 gradient:"$1"-"$2" -fill "$3" \
    -draw "polygon 0,600 $((150 + i * 40)),$((260 + i * 15)) 520,600" -draw "polygon 300,600 $((560 - i * 20)),330 800,600" \
    -quality 88 "$work/Pictures/Holiday/IMG_$((4210 + i)).jpg"
done

mkdir -p "$work/out"
"$acd/.venv/bin/python" scripts/screenshots/acdseen.py "$acd" "$work/Pictures/Holiday" "$work/out"
read -r mx my x0 < "$work/out/menu-at"
o=$work/out
magick "$o/window.png" \( "$o/menu.png" -bordercolor '#9aa0a6' -border 1 \) -geometry +$mx+$my -composite -crop +$x0+0 +repage "$o/w.png"
magick "$o/status.png" -crop "$(magick identify -format %w "$o/w.png")x+0+0" +repage "$o/status.png"
magick "$o/w.png" \( -size 1x14 xc:'#e9ecf1' \) \( "$o/status.png" -bordercolor '#9aa0a6' -border 1 \) \
  -background '#e9ecf1' -gravity west -append -bordercolor '#e9ecf1' -border 20 docs/screenshots/v0.0.17.png
echo "wrote docs/screenshots/v0.0.17.png"

#!/bin/bash
# Acceptance test of the Android app on a running emulator (or device
# reachable as 10.0.2.2 → this machine): one photo, five photos → one
# collection, airplane mode mid-upload, HEIC.
#
#   android/scripts/emulator-test.sh [path/to/photo.heic]
#
# Needs: adb on PATH or $ANDROID_HOME, a booted emulator, Go, jq, ImageMagick.
set -euo pipefail
cd "$(dirname "$0")/../.."
ADB=${ADB:-$(command -v adb || echo "${ANDROID_HOME:-$HOME/Android/Sdk}/platform-tools/adb")}
HEIC=${1:-}
PKG=tibi.android
MAGICK=$(command -v magick || echo convert) # ImageMagick 7 or 6
work=$(mktemp -d)
pid=
trap '[ -n "$pid" ] && kill $pid 2>/dev/null; $ADB shell cmd connectivity airplane-mode disable >/dev/null 2>&1; $ADB emu network speed full >/dev/null 2>&1; rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; $ADB logcat -d -s tibi:* WM-WorkerWrapper:* | tail -20 >&2; exit 1; }

# --- server ---
go build -o "$work/tibi" ./cmd/tibi
port=$((18300 + RANDOM % 500))
export TIBI_DATA_DIR="$work/data" TIBI_LISTEN=127.0.0.1:$port TIBI_BASE_URL=http://10.0.2.2:$port
token=$("$work/tibi" admin token create --name android --scopes upload --config "" 2>/dev/null)
"$work/tibi" serve --config "" 2>"$work/serve.log" &
pid=$!
host=http://127.0.0.1:$port
for _ in $(seq 20); do curl -sf $host/healthz >/dev/null && break; sleep 0.3; done
api() { curl -sf -H "Authorization: Bearer $token" "$host$1"; }
local_url() { sed "s#http://10.0.2.2:$port#$host#"; }

# --- app ---
(cd android && ./gradlew -q assembleDebug assembleDebugAndroidTest)
$ADB install -r -g android/app/build/outputs/apk/debug/app-debug.apk >/dev/null
$ADB install -r android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk >/dev/null
$ADB shell am force-stop $PKG
$ADB shell run-as $PKG sh -c "'rm -rf files/outbox; mkdir -p shared_prefs; cat > shared_prefs/tibi.xml'" <<XML
<?xml version='1.0' encoding='utf-8' standalone='yes' ?>
<map><string name="server">http://10.0.2.2:$port</string><string name="token">$token</string></map>
XML
$ADB shell cmd connectivity airplane-mode disable
$ADB shell input keyevent KEYCODE_WAKEUP
$ADB shell wm dismiss-keyguard
$ADB shell svc power stayon true
$ADB logcat -c

# media: push a file and have it scanned; prints its content URI
add_media() { # local-file device-dir collection-uri
  local name; name=$(basename "$1")
  $ADB push "$1" "/sdcard/$2/$name" >/dev/null
  $ADB shell am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE -d "file:///sdcard/$2/$name" >/dev/null
  for _ in $(seq 30); do
    id=$($ADB shell content query --uri "$3" --projection _id:_display_name | tr -d '\r' | grep "_display_name=$name\$" | sed -E 's/.*_id=([0-9]+),.*/\1/' | tail -1)
    [ -n "$id" ] && { echo "$3/$id"; return; }
    sleep 0.5
  done
  fail "media scan of $name"
}
share() { # mime uri...
  local mime=$1; shift
  if [ $# -eq 1 ]; then
    $ADB shell am start -W -a android.intent.action.SEND -t "$mime" --eu android.intent.extra.STREAM "$1" --grant-read-uri-permission -n $PKG/.ShareActivity >/dev/null
  else
    $ADB shell am instrument -w -e uris "$(IFS=,; echo "$*")" -e class $PKG.ShareIntentTest \
      $PKG.test/androidx.test.runner.AndroidJUnitRunner | grep -q "OK (1 test)" || fail "share of $# files"
  fi
}
# waits for the n-th finished job, prints its URL
done_url() { # n timeout-seconds
  for _ in $(seq $(($2 * 2))); do
    $ADB logcat -d -s tibi:W | grep -q "share failed" && fail "share failed"
    u=$($ADB logcat -d -s tibi:I | grep -oE 'done: \S+' | sed -n "${1}p" | cut -c7-)
    [ -n "$u" ] && { echo "$u"; return; }
    sleep 0.5
  done
  fail "upload $1 did not finish in $2 s"
}
# The clipboard, read the way a user would: paste into the app's server field.
clipboard() {
  $ADB shell am start -W -n $PKG/.SettingsActivity >/dev/null
  sleep 1
  local b
  b=$(ui | grep 'tibi.android:id/server' | grep -oE 'bounds="[^"]*"' | grep -oE '[0-9]+' | tr '\n' ' ')
  set -- $b
  $ADB shell input tap $((($1 + $3) / 2)) $((($2 + $4) / 2))
  $ADB shell input keycombination 113 29 # Ctrl+A
  $ADB shell input keycombination 113 50 # Ctrl+V
  sleep 0.5
  ui | grep 'tibi.android:id/server' | grep -oE ' text="[^"]*"' | head -1 | cut -d'"' -f2
  $ADB shell am force-stop $PKG
}
# The current screen's views, one per line.
ui() {
  for _ in 1 2 3 4 5; do
    $ADB shell rm -f /sdcard/ui.xml
    $ADB shell uiautomator dump /sdcard/ui.xml >/dev/null 2>&1 && break
    sleep 1
  done
  $ADB shell cat /sdcard/ui.xml | tr '>' '\n'
}
sha() { sha256sum | cut -d' ' -f1; }

# 1. One photo → item; link in the clipboard and the notification.
$MAGICK -size 1200x900 gradient:red-blue "$work/one.jpg"
share image/jpeg "$(add_media "$work/one.jpg" Pictures content://media/external/images/media)"
u1=$(done_url 1 30)
id1=${u1##*/}
it=$(api /api/v1/items/$id1)
[ "$(jq -r .kind <<<"$it")/$(jq -r .filename <<<"$it")/$(jq -r .source <<<"$it")" = image/one.jpg/android ] || fail "item: $it"
[ "$(curl -sf "$(jq -r .raw_url <<<"$it" | local_url)" | sha)" = "$(sha < "$work/one.jpg")" ] || fail "photo bytes differ"
notified() { for _ in $(seq 10); do $ADB shell dumpsys notification --noredact 2>/dev/null | grep -q "$1" && return; sleep 1; done; return 1; }
notified "$u1" || fail "no notification with $u1"
# The share dialog shows the link (for sharing it on the spot).
for _ in $(seq 10); do shown=$(ui | grep 'tibi.android:id/url' | grep -oE ' text="[^"]*"' | cut -d'"' -f2); [ "$shown" = "$u1" ] && break; sleep 1; done
[ "$shown" = "$u1" ] || fail "share dialog shows '$shown', want $u1"
ui | grep 'tibi.android:id/share"' >/dev/null || fail "no Share button in the dialog"
clip=$(clipboard)
[ "$clip" = "$u1" ] || fail "clipboard: '$clip', want $u1"
# ...and the app's main screen lists it under recent links.
$ADB shell am start -W -n $PKG/.SettingsActivity >/dev/null
sleep 1
ui | grep -v 'tibi.android:id/server' | grep -q "one.jpg&#10;$u1" || fail "recent links do not list $u1"
$ADB shell am force-stop $PKG
echo "OK: one photo → $u1 (shown in the dialog, in clipboard, notification and recent links)"

# 2. Five photos → one collection.
uris=()
for i in 1 2 3 4 5; do
  $MAGICK -size 800x600 "xc:hsl($((i * 60)),80%,50%)" "$work/five-$i.jpg"
  uris+=("$(add_media "$work/five-$i.jpg" Pictures content://media/external/images/media)")
done
share 'image/*' "${uris[@]}"
u2=$(done_url 2 60)
[[ "$u2" == */c/* ]] || fail "not a collection URL: $u2"
col=$(api /api/v1/collections/${u2##*/})
[ "$(jq '.items | length' <<<"$col")" = 5 ] || fail "collection: $col"
[ "$(jq -r '[.items[].filename] | join(",")' <<<"$col")" = "five-1.jpg,five-2.jpg,five-3.jpg,five-4.jpg,five-5.jpg" ] || fail "order: $col"
[ "$(clipboard)" = "$u2" ] || fail "clipboard after collection"
echo "OK: five photos → one collection $u2 (5 items, in order)"

# 3. Airplane mode in the middle of a big upload; it continues afterwards.
# A ~40 MiB incompressible picture (the app may read images; a video would
# need the gallery's URI grant, which adb cannot give).
$MAGICK -size 3700x3700 xc:red +noise Random -depth 8 -define png:color-type=2 "$work/big.png"
bigsize=$(stat -c %s "$work/big.png")
big=$(add_media "$work/big.png" Pictures content://media/external/images/media)
$ADB emu network speed 6000:6000 >/dev/null # kbit/s: ~1 min for 40 MiB
share image/png "$big"
received() { du -b "$work/data/uploads" 2>/dev/null | tail -1 | cut -f1; }
for _ in $(seq 120); do [ "$(received)" -gt $((12 << 20)) ] && break; sleep 0.5; done
$ADB shell cmd connectivity airplane-mode enable
sleep 3
before=$(received)
echo "   airplane mode on at $((before >> 20)) MiB of $((bigsize >> 20))"
sleep 15
after=$(received)
[ "$after" -lt $((before + (5 << 20))) ] || fail "upload went on in airplane mode ($before → $after)"
$ADB shell dumpsys notification --noredact | grep -q "waiting for the network\|等待网络" && echo "   notification: waiting for the network"
$ADB emu network speed full >/dev/null
$ADB shell cmd connectivity airplane-mode disable
u3=$(done_url 3 180)
it=$(api /api/v1/items/${u3##*/})
# Same pixels (the server strips PNG metadata chunks, so not the same bytes).
curl -sf "$(jq -r .raw_url <<<"$it" | local_url)" -o "$work/big-back.png"
[ "$($MAGICK identify -format %# "$work/big-back.png")" = "$($MAGICK identify -format %# "$work/big.png")" ] || fail "big picture pixels differ"
sent=$(curl -sf $host/metrics | awk '/^tibi_resumable_received_bytes_total/ {print $2}')
small=$(cat "$work"/one.jpg "$work"/five-*.jpg | wc -c)
echo "   server received $((sent >> 20)) MiB in total for $(((bigsize + small) >> 20)) MiB of files"
# At most the chunk in flight is re-sent (4 MiB), not the whole file.
[ "$sent" -le $((bigsize + small + (4 << 20))) ] || fail "the picture was sent again from the start ($sent bytes)"
echo "OK: airplane mode mid-upload → resumed, same pixels ($u3)"

# 4. HEIC (as phones take them) displays: the server converts it.
if [ -z "$HEIC" ] && command -v heif-enc >/dev/null; then
  $MAGICK -size 800x600 plasma:fractal "$work/h.png" && heif-enc -o "$work/gen.heic" "$work/h.png" >/dev/null && HEIC="$work/gen.heic"
fi
if [ -n "$HEIC" ]; then
  cp "$HEIC" "$work/photo.heic"
  share image/heic "$(add_media "$work/photo.heic" Pictures content://media/external/images/media)"
  u4=$(done_url 4 60)
  it=$(api /api/v1/items/${u4##*/})
  mime=$(jq -r .mime <<<"$it"); orig=$(jq -r .orig_mime <<<"$it")
  [ "$mime" = image/jpeg ] && [ "$orig" = image/heic ] || fail "HEIC item: $it"
  curl -sf "$(jq -r .raw_url <<<"$it" | local_url)" | file - | grep -q JPEG || fail "HEIC not served as JPEG"
  echo "OK: HEIC → $u4 served as JPEG $(jq -r '"\(.width)x\(.height)"' <<<"$it")"
else
  echo "SKIP: HEIC (pass a .heic file)"
fi

# 5. Quick Settings tile: uploads the newest screenshot.
$ADB shell am start -W -n $PKG/.SettingsActivity >/dev/null
sleep 1
name=Screenshot_$(date +%Y%m%d-%H%M%S).png
$ADB shell mkdir -p /sdcard/Pictures/Screenshots
$ADB shell screencap -p /sdcard/Pictures/Screenshots/$name
$ADB shell am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE -d file:///sdcard/Pictures/Screenshots/$name >/dev/null
sleep 2
$ADB shell am force-stop $PKG
n=$($ADB logcat -d -s tibi:I | grep -c 'done: ')
$ADB shell cmd statusbar add-tile $PKG/.ScreenshotTileService >/dev/null
$ADB shell cmd statusbar expand-settings >/dev/null # tiles take clicks only while shown
sleep 2
$ADB shell cmd statusbar click-tile $PKG/.ScreenshotTileService >/dev/null
u5=$(done_url $((n + 1)) 60)
it=$(api /api/v1/items/${u5##*/})
[ "$(jq -r .filename <<<"$it")" = "$name" ] && [ "$(jq -r .kind <<<"$it")" = image ] || fail "tile item: $it"
$ADB shell cmd statusbar collapse >/dev/null
echo "OK: Quick Settings tile → $u5 ($name, $(jq -r '"\(.width)x\(.height)"' <<<"$it"))"

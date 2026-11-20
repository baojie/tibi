#!/bin/bash
# The 0.0.16 picture: a photo shared to the Android app, with the link in
# the share dialog. Needs a booted emulator (see android/README.md), adb,
# Go and ImageMagick.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo"
ADB=${ADB:-$(command -v adb || echo "${ANDROID_HOME:-$HOME/Android/Sdk}/platform-tools/adb")}
PKG=tibi.android
work=$(mktemp -d "${TMPDIR:-/tmp}/tibi-shots.XXXXXX")
pid=
trap '[ -n "$pid" ] && kill $pid 2>/dev/null; rm -rf "$work"' EXIT

go build -o "$work/tibi" ./cmd/tibi
port=18110
export TIBI_DATA_DIR="$work/data" TIBI_LISTEN=127.0.0.1:$port TIBI_BASE_URL=http://10.0.2.2:$port
token=$("$work/tibi" admin token create --name phone --scopes upload --config "" 2>/dev/null)
"$work/tibi" serve --config "" 2>"$work/serve.log" &
pid=$!
for _ in $(seq 20); do curl -sf http://127.0.0.1:$port/healthz >/dev/null && break; sleep 0.3; done

(cd android && ./gradlew -q assembleDebug)
$ADB install -r -g android/app/build/outputs/apk/debug/app-debug.apk >/dev/null
$ADB shell am force-stop $PKG
$ADB shell run-as $PKG sh -c "'rm -rf files/outbox; mkdir -p shared_prefs; cat > shared_prefs/tibi.xml'" <<XML
<?xml version='1.0' encoding='utf-8' standalone='yes' ?>
<map><string name="server">http://10.0.2.2:$port</string><string name="token">$token</string></map>
XML
$ADB shell input keyevent KEYCODE_WAKEUP
$ADB shell wm dismiss-keyguard
$ADB shell input keyevent KEYCODE_HOME

# A photo in the gallery.
name=IMG_20261120_181500.jpg
magick -size 1200x1600 gradient:'#87c5ea'-'#f6d6a8' -fill '#ffd27a' -draw 'circle 800,520 920,520' \
  -fill '#2f6b4f' -draw 'polygon 0,1100 380,640 760,1100' -draw 'polygon 420,1100 820,760 1200,1100' \
  \( -size 1200x500 gradient:'#3d8fb8'-'#0b1d33' \) -geometry +0+1100 -composite -quality 88 "$work/$name"
$ADB push "$work/$name" "/sdcard/Pictures/$name" >/dev/null
$ADB shell am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE -d "file:///sdcard/Pictures/$name" >/dev/null
uri=
for _ in $(seq 30); do
  id=$($ADB shell content query --uri content://media/external/images/media --projection _id:_display_name | tr -d '\r' |
    grep "_display_name=$name\$" | sed -E 's/.*_id=([0-9]+),.*/\1/' | tail -1)
  [ -n "$id" ] && { uri=content://media/external/images/media/$id; break; }
  sleep 0.5
done
[ -n "$uri" ] || { echo "media scan failed" >&2; exit 1; }

$ADB shell am start -W -a android.intent.action.SEND -t image/jpeg --eu android.intent.extra.STREAM "$uri" \
  --grant-read-uri-permission -n $PKG/.ShareActivity >/dev/null
for _ in $(seq 60); do
  $ADB shell uiautomator dump /sdcard/ui.xml >/dev/null 2>&1 || true
  $ADB shell cat /sdcard/ui.xml | grep -q "tibi.android:id/url\" [^>]*text=\"http" 2>/dev/null && break
  $ADB shell cat /sdcard/ui.xml | grep -o 'resource-id="tibi.android:id/url"[^>]*' | grep -q 'text="http' && break
  sleep 0.5
done
sleep 0.5
$ADB exec-out screencap -p > "$work/phone.png"
$ADB shell am force-stop $PKG
$ADB shell rm -f "/sdcard/Pictures/$name"

# In a phone-shaped frame on the usual background.
magick "$work/phone.png" -resize x680 \( +clone -alpha extract -draw 'fill black polygon 0,0 0,24 24,0 fill white circle 24,24 24,0' \
  \( +clone -flip \) -compose Multiply -composite \( +clone -flop \) -compose Multiply -composite \) \
  -alpha off -compose CopyOpacity -composite "$work/r.png"
magick "$work/r.png" -bordercolor none -border 0 \( +clone -background '#00000066' -shadow 60x12+0+8 \) +swap \
  -background none -layers merge +repage "$work/s.png"
magick -size 1000x740 xc:'#e9ecf1' "$work/s.png" -gravity center -composite docs/screenshots/v0.0.16.png
echo "wrote docs/screenshots/v0.0.16.png"

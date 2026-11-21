#!/bin/bash
# Regenerate the release screenshots in docs/screenshots/ (one per version,
# showing what that version added; see docs/screenshots/README.md).
#
#   scripts/screenshots/run.sh [VERSION...]     # default: all
#
# Builds tibi, runs throwaway servers on 127.0.0.1:18100–18103 with demo
# data, records real terminal sessions and takes the pictures with headless
# Chrome. Needs Node ≥ 22, Chrome, ImageMagick, ffmpeg, jq and Docker (an
# S3 gateway for 0.0.5, the tibi image for 0.0.11). The extension (0.0.15),
# Android (0.0.16), ACDSeeN (0.0.17) and Homebrew (0.1.2) pictures have their own scripts
# next to this one.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo"
out=$repo/docs/screenshots
mkdir -p "$out"
want=" ${*:-all} "
wanted() { [[ $want == *" all "* || $want == *" $1 "* ]]; }

work=$(mktemp -d "${TMPDIR:-/tmp}/tibi-shots.XXXXXX")
pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  docker rm -f tibi-demo >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$work/bin" "$work/demo" "$work/t"
go build -o "$work/bin/tibi" ./cmd/tibi
export PATH="$work/bin:$PATH" HOME="$work/home"
unset DISPLAY WAYLAND_DISPLAY TIBI_CONFIG
mkdir -p "$HOME"
demo=$work/demo
scenes=$work/scenes.json
echo "[" > "$scenes"
scene() { echo "$1," >> "$scenes"; }

# start NAME PORT [ENV...]: a tibi server with its own data dir.
start() {
  local name=$1 port=$2
  shift 2
  mkdir -p "$work/$name"
  env TIBI_DATA_DIR="$work/$name" TIBI_LISTEN=127.0.0.1:$port TIBI_BASE_URL=http://localhost:$port "$@" \
    tibi serve --config "${CFG:-}" 2>>"$work/$name.log" &
  pids+=($!)
  for _ in $(seq 50); do curl -sf "http://localhost:$port/healthz" >/dev/null && return; sleep 0.2; done
  echo "server $name did not start" >&2; cat "$work/$name.log" >&2; exit 1
}
token() { # token NAME PORT SCOPES
  TIBI_DATA_DIR="$work/$1" tibi admin token create --name "$4" --scopes "$3" --config "" 2>/dev/null
}

# A transcript: say CMD runs CMD in the demo dir and records "$ CMD" and
# its output; note TEXT records a comment line.
T=
term() { T=$work/t/$1.txt; : > "$T"; }
say() { printf '$ %s\n' "$1" >> "$T"; (cd "$demo" && eval "$1") >> "$T" 2>&1 || true; }
note() { printf '# %s\n' "$1" >> "$T"; }
blank() { echo >> "$T"; }

# --- demo files ---
cd "$demo"
printf 'Meeting notes\n- ship 0.1.0\n- write the docs\n' > notes.txt
# scene W H SKY_TOP SKY_BOTTOM SUN SEA HILLS OUT: a flat-colour landscape.
landscape() {
  local w=$1 h=$2
  magick -size "${w}x$((h * 3 / 5))" gradient:"$3"-"$4" \
    \( -size "${w}x$((h - h * 3 / 5))" gradient:"$6"-'#0b1d33' \) -append \
    -fill "$5" -draw "circle $((w * 2 / 3)),$((h * 2 / 5)) $((w * 2 / 3 + h / 10)),$((h * 2 / 5))" \
    -fill "$7" -draw "polygon 0,$((h * 3 / 5)) $((w / 5)),$((h * 2 / 5)) $((w * 2 / 5)),$((h * 3 / 5))" \
    -draw "polygon $((w / 4)),$((h * 3 / 5)) $((w * 9 / 20)),$((h * 9 / 20)) $((w * 3 / 5)),$((h * 3 / 5))" \
    -fill 'rgba(255,255,255,0.18)' -draw "rectangle $((w * 2 / 3 - h / 9)),$((h * 3 / 5 + 12)) $((w * 2 / 3 + h / 9)),$((h * 3 / 5 + 16))" \
    -draw "rectangle $((w * 2 / 3 - h / 14)),$((h * 3 / 5 + 32)) $((w * 2 / 3 + h / 14)),$((h * 3 / 5 + 35))" \
    -quality 88 "$8"
}
landscape 1400 900 '#2b1055' '#f6a05b' '#ffd27a' '#d06b4a' '#1d1238' sunset.jpg
landscape 480 320 '#87c5ea' '#e8f4fb' '#fff3b0' '#3d8fb8' '#2f6b4f' a.jpg
landscape 480 320 '#0d1b3e' '#5a4a8a' '#f4f1de' '#26305c' '#141a33' b.jpg
landscape 480 320 '#f9c784' '#fcefd4' '#e8613c' '#7fb7be' '#5b7553' c.png
cat > server.go <<'EOF'
package main

import (
	"fmt"
	"log"
	"net/http"
)

// greet answers every request with a friendly message.
func greet(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "world"
	}
	fmt.Fprintf(w, "hello, %s\n", name)
}

func main() {
	http.HandleFunc("/", greet)
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
EOF
cat > poem.txt <<'EOF'
题西林壁
横看成岭侧成峰，远近高低各不同。
不识庐山真面目，只缘身在此山中。
EOF
cat > config.py <<'EOF'
def parse_config(path):
    """Read key = value lines into a dict."""
    out = {}
    for line in open(path):
        key, _, value = line.partition("=")
        out[key.strip()] = value.strip()
    return out
EOF
ffmpeg -loglevel error -f lavfi -i testsrc2=size=1280x720:rate=30 -f lavfi -i sine=frequency=440 \
  -t 8 -pix_fmt yuv420p -c:v libx264 -c:a aac -shortest clip.mp4
cd "$repo"

start main 18100
TIBI_TOKEN=$(token main 18100 upload,delete,read_private laptop)
PHONE_TOKEN=$(token main 18100 upload phone)
export TIBI_SERVER=http://localhost:18100 TIBI_TOKEN PHONE_TOKEN
base=$TIBI_SERVER

# 0.0.1 — upload from the CLI and curl, download the same bytes.
if wanted 0.0.1; then
  term v0.0.1
  say 'tibi up notes.txt'
  say "echo 'hello from curl' | curl -s -H \"Authorization: Bearer \$TIBI_TOKEN\" --data-binary @- $base/api/v1/items | jq -r .url"
  id=$(echo 'hello from curl' | curl -s -H "Authorization: Bearer $TIBI_TOKEN" --data-binary @- $base/api/v1/items | jq -r .id)
  say "curl -s $base/$id/dl"
  say "curl -s $base/robots.txt"
  say "curl -s -o /dev/null -w '%{http_code}\\n' -H 'Authorization: Bearer wrong' --data-binary @notes.txt $base/api/v1/items"
  scene "{\"out\":\"$out/v0.0.1.png\",\"term\":\"$T\",\"title\":\"tibi — upload from anywhere\"}"
fi

# 0.0.2 — an image page.
if wanted 0.0.2; then
  u=$(cd "$demo" && tibi up -t "Harbour at dusk" sunset.jpg)
  scene "{\"out\":\"$out/v0.0.2.png\",\"url\":\"$u\",\"width\":1100,\"height\":760,\"wait\":\"document.querySelector('img') && document.querySelector('img').complete\"}"
fi

# 0.0.3 — a highlighted paste.
if wanted 0.0.3; then
  u=$(cd "$demo" && tibi paste -t "server.go" < server.go)
  scene "{\"out\":\"$out/v0.0.3.png\",\"url\":\"$u\",\"width\":1100,\"height\":760}"
fi

# 0.0.4 — burn after reading: the page asks before revealing.
if wanted 0.0.4; then
  u=$(echo 'The door code is 4821.' | tibi paste --burn -t "door code")
  scene "{\"out\":\"$out/v0.0.4.png\",\"url\":\"$u\",\"width\":1100,\"height\":560}"
fi

# 0.0.5 — move the blobs to S3, serve with presigned redirects.
if wanted 0.0.5; then
  : "${S3_ENDPOINT:=http://127.0.0.1:19000}"
  if ! curl -s -o /dev/null "$S3_ENDPOINT"; then
    docker rm -f tibi-s3 >/dev/null 2>&1 || true
    docker run -d --name tibi-s3 -p 127.0.0.1:19000:7070 versity/versitygw:v1.11.0 \
      --access tibitest --secret tibitestsecret posix /tmp/vgw >/dev/null
    sleep 2
  fi
  docker exec tibi-s3 sh -c 'rm -rf /tmp/vgw/tibi-demo/tibi && mkdir -p /tmp/vgw/tibi-demo' 
  s3env=(TIBI_S3_ENDPOINT=$S3_ENDPOINT TIBI_S3_BUCKET=tibi-demo TIBI_S3_PREFIX=tibi/ TIBI_S3_REGION=us-east-1
    TIBI_S3_ACCESS_KEY=tibitest TIBI_S3_SECRET_KEY=tibitestsecret)
  img=$(cd "$demo" && tibi up --raw a.jpg)
  term v0.0.5
  note 'storage.backend = "fs" → "s3" (Backblaze B2, R2, MinIO, …)'
  say "tibi admin blobs migrate --to s3"
  # The demo runs migrate against the scratch data dir with the S3 settings.
  sed -i '$d' "$T"
  (env TIBI_DATA_DIR="$work/main" "${s3env[@]}" tibi admin blobs migrate --to s3 --config "" 2>&1) >> "$T" || true
  kill "${pids[0]}"; wait "${pids[0]}" 2>/dev/null || true
  start main 18100 TIBI_STORAGE_BACKEND=s3 TIBI_BLOB_DELIVERY=redirect "${s3env[@]}"
  note 'blob_delivery = "redirect": images go straight to the bucket'
  say "curl -sI $img | grep -iE '^(HTTP|location)' | cut -c1-110"
  say "curl -s -o /dev/null -w '%{http_code} %{size_download} bytes\\n' -L -r 0-99 $img"
  scene "{\"out\":\"$out/v0.0.5.png\",\"term\":\"$T\",\"title\":\"tibi on S3 / Backblaze B2\"}"
  kill "${pids[-1]}"; wait "${pids[-1]}" 2>/dev/null || true
  start main 18100 TIBI_STORAGE_BACKEND=s3 "${s3env[@]}"
fi

# 0.0.6 — video with a player that seeks.
if wanted 0.0.6; then
  u=$(cd "$demo" && tibi up -t "test pattern" clip.mp4)
  scene "{\"out\":\"$out/v0.0.6.png\",\"url\":\"$u\",\"width\":1100,\"height\":760,\"wait\":\"document.querySelector('video') && document.querySelector('video').readyState >= 1\",\"run\":\"document.querySelector('video').currentTime = 3\",\"waitAfter\":\"document.querySelector('video').readyState >= 2\",\"settle\":1200}"
fi

# 0.0.7 — search: English, code identifiers, Chinese substrings.
if wanted 0.0.7; then
  (cd "$demo" && tibi paste -t "题西林壁" < poem.txt && tibi paste -t "config loader" < config.py) >/dev/null
  term v0.0.7
  say 'tibi search 庐山'
  say 'tibi search parse_config'
  say 'tibi search harbour'
  scene "{\"out\":\"$out/v0.0.7.png\",\"term\":\"$T\",\"title\":\"tibi search\"}"
fi

# 0.0.8 — an album.
if wanted 0.0.8; then
  u=$(cd "$demo" && tibi up --album -t "Weekend trip" a.jpg b.jpg c.png notes.txt)
  scene "{\"out\":\"$out/v0.0.8.png\",\"url\":\"$u\",\"width\":1100,\"height\":800,\"wait\":\"[...document.images].every(i => i.complete)\"}"
fi

# 0.0.9 — the upload page in Chinese, after pasting a screenshot.
if wanted 0.0.9; then
  run="localStorage.setItem('tibi.token', '$TIBI_TOKEN'); location.reload()"
  paste="(async () => { const b = await (await fetch('$(cd "$demo" && tibi up --raw b.jpg)')).blob();
    const dt = new DataTransfer(); dt.items.add(new File([b], 'screenshot.png', { type: b.type }));
    document.body.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); })()"
  cat > "$work/paste.js" <<<"$paste"
  scene "$(jq -nc --arg out "$out/v0.0.9.png" --arg url "$base/upload" --arg run "$run" --arg paste "$paste" \
    '{out:$out,url:$url,lang:"zh-CN",width:1100,height:760,run:$run,waitAfter:"!document.getElementById(\"uploader\").hidden",
      then:$paste,waitThen:"document.querySelector(\"#results .result-link\")"}')"
fi

# 0.0.10 — over the daily bandwidth cap, the page explains.
if wanted 0.0.10; then
  echo 'max_item_bytes_per_day = 100000' > "$work/capped.toml"
  CFG=$work/capped.toml start capped 18102
  ct=$(token capped 18102 upload demo)
  j=$(cd "$demo" && TIBI_SERVER=http://localhost:18102 TIBI_TOKEN=$ct tibi up --json -t "popular picture" sunset.jpg)
  u=$(jq -r .url <<<"$j")
  for _ in 1 2 3 4; do curl -s -o /dev/null "$(jq -r .raw_url <<<"$j")"; done
  scene "{\"out\":\"$out/v0.0.10.png\",\"url\":\"$u\",\"width\":1100,\"height\":600}"
fi

# 0.0.11 — the Docker image: HEIC from a phone comes back as JPEG.
if wanted 0.0.11; then
  ver=$(sed -n 's/^var Version = "\(.*\)"/\1/p' internal/app/app.go)
  docker build -q -t "tibi:$ver" . >/dev/null
  cp scripts/screenshots/IMG_0042.heic "$demo/"
  docker rm -f tibi-demo >/dev/null 2>&1 || true
  term v0.0.11
  say "docker run -d --name tibi-demo -p 127.0.0.1:18103:8070 -e TIBI_BASE_URL=http://localhost:18103 tibi:$ver | cut -c1-12"
  sleep 2
  say "docker exec tibi-demo tibi admin token create --name phone --scopes upload"
  dt=$(grep -o "tibi_[A-Za-z0-9]*" "$T" | tail -1)
  say 'curl -s localhost:18103/healthz'
  printf '$ %s\n' 'tibi up IMG_0042.heic' >> "$T"
  (cd "$demo" && TIBI_SERVER=http://localhost:18103 TIBI_TOKEN=$dt tibi up --json IMG_0042.heic) > "$work/heic.json"
  jq -r .url "$work/heic.json" >> "$T"
  say "curl -s -o /dev/null -w '%{content_type}\\n' $(jq -r .raw_url "$work/heic.json")"
  scene "{\"out\":\"$out/v0.0.11.png\",\"term\":\"$T\",\"title\":\"tibi in Docker\"}"
fi

# 0.0.12 — the MCP server, spoken to over stdio.
if wanted 0.0.12; then
  cat > "$demo/session.jsonl" <<'EOF2'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"demo","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"paste_text","arguments":{"text":"SELECT 1;","lang":"sql","expires":"7d"}}}
EOF2
  term v0.0.12
  note 'claude mcp add tibi -- tibi mcp      (any MCP client works)'
  say 'tibi mcp < session.jsonl > replies.jsonl'
  say "jq -r 'select(.id == 2) | .result.tools[] | \"\\(.name): \\(.description | split(\".\")[0])\"' replies.jsonl"
  say "jq -r 'select(.id == 3) | .result.content[0].text' replies.jsonl"
  scene "{\"out\":\"$out/v0.0.12.png\",\"term\":\"$T\",\"title\":\"tibi mcp\",\"width\":1100}"
fi

# 0.0.13 — a big upload interrupted and resumed (through a throttling
# proxy, so it takes a few seconds).
if wanted 0.0.13; then
  node scripts/screenshots/slow.mjs 18104 18100 $((120 << 20)) &
  pids+=($!)
  sleep 0.5
  head -c $((600 << 20)) /dev/urandom > "$demo/dataset.tar"
  tty() { # run CMD with a terminal on stderr, keep the last state of each \r-updated line
    printf '$ %s\n' "$1" >> "$T"
    (cd "$demo" && TIBI_SERVER=http://localhost:18104 script -qec "$2" /dev/null) | tr -d '\033' |
      python3 -c 'import sys
for l in sys.stdin.read().replace("\r\n","\n").split("\n"):
    l=l.split("\r")
    l=[x for x in l if x.strip()]
    print(l[-1].rstrip() if l else "")' | sed '/^$/d; /not copied: clipboard/d' >> "$T" || true
  }
  term v0.0.13
  tty 'tibi up dataset.tar' 'timeout -s INT 2.2 tibi up dataset.tar'
  sed -i '$s/$/ ^C/' "$T"
  tty 'tibi up dataset.tar' 'tibi up dataset.tar'
  url=$(tail -1 "$T")
  url=$(grep http "$T" | tail -1)
  say "curl -s $url/dl | sha256sum | cut -c1-16; sha256sum dataset.tar | cut -c1-16"
  scene "{\"out\":\"$out/v0.0.13.png\",\"term\":\"$T\",\"title\":\"resumable uploads\"}"
  rm -f "$demo/dataset.tar"
fi

# 0.0.14 — an end-to-end encrypted paste, decrypted in the browser.
if wanted 0.0.14; then
  u=$(printf '# staging credentials (example)\nDATABASE_URL=postgres://app:example@db.internal/app\nSMTP_PASSWORD=correct-horse-battery\n' |
    TIBI_E2E_PASSWORD=open-sesame tibi paste --encrypt --password -l ini -n staging.env)
  scene "$(jq -nc --arg out "$out/v0.0.14.png" --arg url "$u" '{out:$out,url:$url,width:1100,height:560,
    wait:"!document.getElementById(\"e2e-password\").hidden",
    run:"document.querySelector(\"#e2e-password [name=password]\").value = \"open-sesame\"; document.getElementById(\"e2e-password\").requestSubmit()",
    waitAfter:"document.querySelector(\"#e2e-content pre\")"}')"
fi

# 0.1.0 — private items, signed links, rate limits.
if wanted 0.1.0; then
  printf 'Lease agreement (draft)\n' > "$demo/lease.txt"
  term v0.1.0
  note 'a private item: invisible without the right token'
  say 'tibi up --private lease.txt'
  pu=$(tail -1 "$T"); pid=${pu##*/}
  say "curl -s -o /dev/null -w '%{http_code}\\n' $pu"
  say "curl -s -H \"Authorization: Bearer \$PHONE_TOKEN\" $base/api/v1/items/$pid | jq -c .error"
  say "curl -s -o /dev/null -w '%{http_code}\\n' \"\$(tibi share $pid --ttl 1h 2>/dev/null)\""
  note 'every token is rate limited (default 1200/min, burst 200)'
  say "for i in \$(seq 300); do curl -s -o /dev/null -w '%{http_code}\\n' -H \"Authorization: Bearer \$PHONE_TOKEN\" $base/api/v1/whoami; done | sort | uniq -c"
  scene "{\"out\":\"$out/v0.1.0.png\",\"term\":\"$T\",\"title\":\"private items, signed links, rate limits\",\"width\":1100}"
fi

# 0.1.1 — the upload page labels its two copy buttons: link and Markdown.
if wanted 0.1.1; then
  run="localStorage.setItem('tibi.token', '$TIBI_TOKEN'); location.reload()"
  paste="(async () => { const b = await (await fetch('$(cd "$demo" && tibi up --raw a.jpg)')).blob();
    const dt = new DataTransfer(); dt.items.add(new File([b], 'screenshot.png', { type: b.type }));
    document.body.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); })()"
  scene "$(jq -nc --arg out "$out/v0.1.1.png" --arg url "$base/upload" --arg run "$run" --arg paste "$paste" \
    '{out:$out,url:$url,width:1100,height:760,run:$run,waitAfter:"!document.getElementById(\"uploader\").hidden",
      then:$paste,waitThen:"document.querySelector(\"#results .result-link\")"}')"
fi

echo "{}]" >> "$scenes"
jq -c '[.[] | select(.out)]' "$scenes" > "$scenes.ok"
node "$repo/scripts/screenshots/shot.mjs" "$scenes.ok" "${CHROME:-google-chrome}"

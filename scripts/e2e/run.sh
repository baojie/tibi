#!/bin/sh
# Build tibi, start it on a scratch data dir, and run the browser tests.
# Needs Node ≥ 22 and Chrome (CHROME=… to override).
set -eu
cd "$(dirname "$0")/../.."
work=$(mktemp -d)
trap 'kill $pid 2>/dev/null || true; rm -rf "$work"' EXIT
go build -o "$work/tibi" ./cmd/tibi
export TIBI_DATA_DIR="$work/data" TIBI_LISTEN=127.0.0.1:18099 TIBI_BASE_URL=http://127.0.0.1:18099
token=$("$work/tibi" admin token create --name e2e --scopes upload,delete --config "" 2>/dev/null)
"$work/tibi" serve --config "" 2>"$work/serve.log" &
pid=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do curl -sf "$TIBI_BASE_URL/healthz" >/dev/null && break; sleep 0.5; done
chrome=${CHROME:-google-chrome}

node scripts/e2e/paste-upload.mjs "$TIBI_BASE_URL" "$token" "$chrome"

# Encrypted items made by the CLI, decrypted by the browser.
export TIBI_SERVER="$TIBI_BASE_URL" TIBI_TOKEN="$token" HOME="$work"
text=$(printf 'package main\n\nfunc main() { println("secret") }\n' | "$work/tibi" paste --encrypt -l go --no-copy)
pw=$(printf 'protected with a password\n' | TIBI_E2E_PASSWORD=open-sesame "$work/tibi" paste --encrypt --password --no-copy)
printf 'iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAYAAAD0In+KAAAAEUlEQVR42mP8z8DwnxEImAQAG/0C/xTPyMEAAAAASUVORK5CYII=' | base64 -d > "$work/pixel.png"
img=$("$work/tibi" up --encrypt --no-copy "$work/pixel.png")
node scripts/e2e/decrypt.mjs "$text" "$pw" open-sesame "$img" "$chrome"

# The Chrome extension (starts its own servers, CORS limited to its origin).
node scripts/e2e/extension.mjs "$work/tibi" "$chrome"

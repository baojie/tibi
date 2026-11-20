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
node scripts/e2e/paste-upload.mjs "$TIBI_BASE_URL" "$token" "${CHROME:-google-chrome}"

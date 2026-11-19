#!/bin/sh
# Run the test suite against a local S3-compatible server.
#
# Starts a throwaway versitygw container (S3 gateway over a POSIX dir) on
# 127.0.0.1:19000 if none is running, then runs `go test` with
# TIBI_TEST_S3_* set. Settings can be overridden in private/s3test.env.
#
#   scripts/test-s3.sh                 # all packages
#   scripts/test-s3.sh -run Migrate ./internal/app/
set -eu
cd "$(dirname "$0")/.."

TIBI_TEST_S3_ENDPOINT=http://127.0.0.1:19000
TIBI_TEST_S3_ACCESS_KEY=tibitest
TIBI_TEST_S3_SECRET_KEY=tibitestsecret
TIBI_TEST_S3_BUCKET=tibi-test
[ -f private/s3test.env ] && . ./private/s3test.env
export TIBI_TEST_S3_ENDPOINT TIBI_TEST_S3_ACCESS_KEY TIBI_TEST_S3_SECRET_KEY TIBI_TEST_S3_BUCKET

IMAGE=${TIBI_TEST_S3_IMAGE:-versity/versitygw:v1.11.0}
if ! curl -s -o /dev/null "$TIBI_TEST_S3_ENDPOINT"; then
  docker rm -f tibi-s3 >/dev/null 2>&1 || true
  docker run -d --name tibi-s3 -p 127.0.0.1:19000:7070 "$IMAGE" \
    --access "$TIBI_TEST_S3_ACCESS_KEY" --secret "$TIBI_TEST_S3_SECRET_KEY" posix /tmp/vgw >/dev/null
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    curl -s -o /dev/null "$TIBI_TEST_S3_ENDPOINT" && break
    sleep 1
  done
fi

if [ $# -eq 0 ]; then set -- ./...; fi
exec go test -count=1 "$@"

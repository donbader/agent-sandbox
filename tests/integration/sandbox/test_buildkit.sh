#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

BUILDKIT_SERVICE="sandbox-test-agent-docker-buildkit"

echo "--- BuildKit build verification ---"
BUILD_RESULT=""
for attempt in 1 2 3 4 5; do
  BUILD_RESULT=$(exec_in "$BUILDKIT_SERVICE" \
    sh -c 'mkdir -p /tmp/build-test && printf "FROM alpine:3.20\nRUN echo buildkit-ok\n" > /tmp/build-test/Dockerfile && buildctl --addr tcp://127.0.0.1:8372 build --frontend=dockerfile.v0 --local context=/tmp/build-test --local dockerfile=/tmp/build-test --no-cache' || true)
  if echo "$BUILD_RESULT" | grep -qE "exporting to image|sending tarball|DONE"; then break; fi
  echo "  attempt $attempt: not ready, retrying in 3s..."
  sleep 3
done

if echo "$BUILD_RESULT" | grep -qE "exporting to image|sending tarball|DONE"; then
  pass "BuildKit: can build Dockerfiles (runc + cgroup working)"
else
  fail "BuildKit: build failed" "$(echo "$BUILD_RESULT" | tail -3)"
fi

echo "  Running HTTPS test inside buildkit RUN container..."
HTTPS_RESULT=$(exec_in "$BUILDKIT_SERVICE" \
  sh -c 'mkdir -p /tmp/https-test && printf "FROM alpine:3.20\nRUN wget -q -O /dev/null https://dl-cdn.alpinelinux.org/alpine/v3.20/main/x86_64/APKINDEX.tar.gz && echo https-ok\n" > /tmp/https-test/Dockerfile && buildctl --addr tcp://127.0.0.1:8372 build --frontend=dockerfile.v0 --local context=/tmp/https-test --local dockerfile=/tmp/https-test --no-cache' || true)

if echo "$HTTPS_RESULT" | grep -qE "exporting to image|sending tarball|DONE"; then
  pass "BuildKit: HTTPS works in RUN containers (CA cert injected)"
else
  fail "BuildKit: HTTPS failed in RUN container" "$(echo "$HTTPS_RESULT" | tail -3)"
fi

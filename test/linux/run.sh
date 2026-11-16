#!/usr/bin/env bash
# Builds the Linux test image and runs the input/focus integration tests in
# a privileged container (for /dev/uinput) with Xvfb. Exits non-zero on any
# failure. Extra arguments go to `go test` (e.g. -run TestUinput).
#
#   test/linux/run.sh
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/../.." && pwd)
image=deckchat-linux-test

docker build -q -t "$image" "$here" >/dev/null
docker run --rm --privileged \
  -v "$repo/companion:/src/companion:ro" \
  -v deckchat-gomod:/go/pkg/mod \
  -v deckchat-gocache:/root/.cache/go-build \
  "$image" deckchat-in-container "$@"

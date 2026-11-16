#!/usr/bin/env bash
# Runs inside the test image (see run.sh). Starts three Xvfb servers and runs
# the linuxintegration tests for internal/input, internal/focus and the
# end-to-end service test in cmd/deckchat.
#   :99  main display (clipboard, focus properties, end-to-end sender)
#   :98  fresh display nobody touches (gamescope atoms never interned)
#   :97  display that requires an MIT-MAGIC-COOKIE-1 from an Xauthority file
set -euo pipefail

[ -c /dev/uinput ] || { echo "no /dev/uinput: run the container with --privileged" >&2; exit 1; }

# Create the socket dir up front: parallel Xvfb starts race on mkdir.
mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix
: >/tmp/xauth97
cookie=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')
xauth -q -f /tmp/xauth97 add :97 MIT-MAGIC-COOKIE-1 "$cookie"

Xvfb :99 -screen 0 640x480x24 -nolisten tcp -noreset >/tmp/xvfb99.log 2>&1 &
Xvfb :98 -screen 0 640x480x24 -nolisten tcp -noreset >/tmp/xvfb98.log 2>&1 &
Xvfb :97 -screen 0 640x480x24 -nolisten tcp -noreset -auth /tmp/xauth97 >/tmp/xvfb97.log 2>&1 &

for d in 99 98 97; do
  for _ in $(seq 100); do [ -S /tmp/.X11-unix/X$d ] && break; sleep 0.05; done
  [ -S /tmp/.X11-unix/X$d ] || { echo "Xvfb :$d did not start" >&2; cat /tmp/xvfb$d.log >&2; exit 1; }
done
XAUTHORITY=/tmp/xauth97 xprop -display :97 -root >/dev/null

export DISPLAY=:99
export DECKCHAT_FRESH_DISPLAY=:98
export DECKCHAT_AUTH_DISPLAY=:97 DECKCHAT_AUTH_FILE=/tmp/xauth97

go version
uname -srm
# -p 1: packages share the Xvfb CLIPBOARD and focus properties on :99.
go test -p 1 -tags linuxintegration -race -count=1 -v "$@" ./internal/input/... ./internal/focus/... ./cmd/...

#!/usr/bin/env bash
# Smoke-tests install.sh and uninstall.sh from the release archive in an
# x86-64 container, with systemctl/loginctl/sudo/udevadm stubbed (there's no
# systemd in a container). Needs `make release` first.
#
#   test/linux/install-smoke.sh
set -euo pipefail
repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
[ -f "$repo/dist/deckchat.tar.gz" ] || { echo "run make release first" >&2; exit 1; }

docker run --rm --platform linux/amd64 -v "$repo/dist:/dist:ro" debian:bookworm-slim bash -euo pipefail -c '
  apt-get update -qq >/dev/null && apt-get install -y -qq sudo >/dev/null
  useradd -m deck
  printf "Defaults secure_path=/stubs:/usr/sbin:/usr/bin:/sbin:/bin\ndeck ALL=(ALL) NOPASSWD: ALL\n" >/etc/sudoers.d/deck
  mkdir -p /stubs
  for c in systemctl loginctl udevadm; do printf "#!/bin/sh\necho \"[stub] $c \$*\" >>/tmp/calls\n" >/stubs/$c; done
  chmod +x /stubs/*
  chmod 666 /dev/null; touch /tmp/calls; chmod 666 /tmp/calls

  su deck -s /bin/bash -c "
    set -euo pipefail
    export PATH=/stubs:\$PATH
    root=\$HOME/.local/share/Steam/steamapps/compatdata/123/pfx/drive_c/Program\ Files\ \(x86\)/World\ of\ Warcraft/_forever_
    mkdir -p \"\$root/Interface/AddOns\" \"\$root/Logs\"
    cd \$HOME && tar -xzf /dist/deckchat.tar.gz && cd deckchat
    ./install.sh           # doctor fails without Game Mode, but install.sh continues
    test -f /etc/udev/rules.d/70-deckchat-uinput.rules
    test -x \$HOME/.local/bin/deckchat
    test -f \"\$root/Interface/AddOns/DeckChat/DeckChat.toc\"
    test -f \$HOME/.config/systemd/user/deckchat.service
    test -f \$HOME/.config/deckchat/secret
    [ \"\$(stat -c %a \$HOME/.config/deckchat/secret)\" = 600 ]
    test \"\$(\$HOME/.local/bin/deckchat addon-dir)\" = \"\$root/Interface/AddOns/DeckChat\"
    ./uninstall.sh
    test ! -e \$HOME/.local/bin/deckchat
    test ! -e \"\$root/Interface/AddOns/DeckChat\"
    test ! -e \$HOME/.config/systemd/user/deckchat.service
    test ! -e \$HOME/.config/deckchat
    test ! -e /etc/udev/rules.d/70-deckchat-uinput.rules
    echo INSTALL-SMOKE-OK
  "
  echo "--- stubbed calls"; cat /tmp/calls
'

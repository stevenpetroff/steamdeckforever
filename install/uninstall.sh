#!/usr/bin/env bash
# Removes everything install.sh added:  ./uninstall.sh
set -uo pipefail
bin="$HOME/.local/bin/deckchat"
rule=/etc/udev/rules.d/70-deckchat-uinput.rules

if [ -x "$bin" ]; then
  addon_dir=$("$bin" addon-dir 2>/dev/null || true)
fi

systemctl --user disable --now deckchat.service 2>/dev/null
rm -f "$HOME/.config/systemd/user/deckchat.service"
systemctl --user daemon-reload 2>/dev/null

if [ -n "${addon_dir:-}" ] && [ "$(basename "$addon_dir")" = DeckChat ]; then
  rm -rf "$addon_dir"
  echo "Removed add-on: $addon_dir"
fi

if [ -f "$rule" ]; then
  sudo rm -f "$rule" && sudo udevadm control --reload-rules
fi

rm -f "$bin"
rm -rf "$HOME/.config/deckchat"
echo "DeckChat removed. (Lingering was left on; turn it off with: loginctl disable-linger $USER)"

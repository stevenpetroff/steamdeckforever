#!/usr/bin/env bash
# Installs DeckChat for the current user on SteamOS. Run it from the unpacked
# release folder in Desktop Mode (Konsole):  ./install.sh
# It makes no network connections.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
bin="$HOME/.local/bin/deckchat"
unit_dir="$HOME/.config/systemd/user"
rule=/etc/udev/rules.d/70-deckchat-uinput.rules

say() { printf '\n==> %s\n' "$*"; }

say "Installing deckchat to $bin"
install -Dm755 "$here/deckchat" "$bin"

say "Checking the virtual keyboard (/dev/uinput)"
if [ -w /dev/uinput ]; then
  echo "Already writable; no system change needed."
else
  echo "DeckChat needs one system rule so it can create its virtual keyboard."
  if ! sudo -v; then
    cat <<'EOF'

sudo needs a password, and the Steam Deck has none by default.
Set one with:   passwd
Then run ./install.sh again.
EOF
    exit 1
  fi
  sudo install -Dm644 "$here/70-deckchat-uinput.rules" "$rule"
  sudo udevadm control --reload-rules
  sudo udevadm trigger --name-match=uinput
fi

say "Installing the WoW add-on"
if ! addon_dir=$("$bin" install-addon "$here/addon/DeckChat"); then
  echo "Couldn't find WoW Forever. Launch it once through Steam, then run ./install.sh again." >&2
  exit 1
fi
echo "$addon_dir"

say "Starting the background service"
install -Dm644 "$here/deckchat.service" "$unit_dir/deckchat.service"
# Lingering starts the service at boot, before the Game Mode session.
loginctl enable-linger "$USER" 2>/dev/null || sudo loginctl enable-linger "$USER" || true
systemctl --user daemon-reload
systemctl --user enable --now deckchat.service
systemctl --user restart deckchat.service

# Give the service a moment to write the pairing file.
for _ in $(seq 20); do [ -f "$addon_dir/Pairing.lua" ] && break; sleep 0.5; done

say "Checking everything"
"$bin" doctor || echo "(The game display check passes only while Game Mode is running.)"

cat <<'EOF'

Done. Next:
  1. Switch to Game Mode and launch WoW Forever (restart it if it was open —
     a newly installed add-on isn't picked up by /reload).
  2. In chat, type:  /deckchat pair
  3. Scan the QR code with your phone's camera. Keep your phone on the same Wi-Fi.
EOF

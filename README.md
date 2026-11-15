# DeckChat

Read and type WoW Forever chat on your phone while you play on a Steam Deck, handheld or docked.

- Your phone shows live chat in WoW's colors. Tap a whisper to reply.
- Type with your phone's keyboard (autocorrect, swipe, dictation, emoji), tap **Send**, and the message goes into WoW's own chat box. `/w`, `/g`, `/p`, `/r` and your current channel all work as in game.
- Everything stays on your Deck and phone. No account, no cloud, no internet connection is used.

## How it works

| Part | What it does |
|---|---|
| `DeckChat` add-on | Keeps WoW's chat log on and flushed every 2 s. Adds a hidden key that opens a clean chat box. Shows the pairing QR code. |
| `deckchat` service | Reads the chat log and serves the phone page on your Wi-Fi. To send, it puts your message on the clipboard, then presses the add-on's key, Ctrl+V and Enter through a virtual keyboard. |
| Phone page | Opens in your phone's browser. No app to install. |

It sends only when WoW is the focused window, and only one message per tap. It never types game keys.

## Install

1. On the Deck, switch to **Desktop Mode** and open **Konsole**.
2. Unpack the release and run the installer:

   ```sh
   tar -xzf deckchat.tar.gz && cd deckchat && ./install.sh
   ```

   If it asks for a password and you've never set one, run `passwd` first, then rerun `./install.sh`. The password is only needed if the Deck's virtual-keyboard permission isn't already in place.
3. Back in **Game Mode**, start (or restart) WoW Forever.
4. In chat, type `/deckchat pair` and scan the QR code with your phone. Keep the phone on the same Wi-Fi.

## Commands

- In WoW:
  - `/deckchat pair` shows the pairing code.
  - `/deckchat status` shows chat logging.
  - `/deckchat flush 5` changes the flush interval (1–30 s).
- On the Deck:
  - `deckchat doctor` checks the install.
  - `deckchat pair --reset` signs out every paired phone and makes a new code (then `/reload` and pair again).
  - `deckchat send "/s hi"` is a test send.

## Troubleshooting

- **Phone says "Not paired":** in WoW, run `/deckchat pair` and scan again.
- **"WoW isn't in front":** close the Steam overlay or keyboard and tap back into the game.
- **"Keyboard unavailable":** run `./install.sh` again. A SteamOS update can remove the permission rule.
- **The phone can't connect after the Deck changed networks:** `/reload` in WoW, then pair again.
- Service logs: `journalctl --user -u deckchat -f`.

## Uninstall

Run `./uninstall.sh` from the release folder.

## Notes

- Blizzard's Terms of Use restrict third-party software that sends input to the game. DeckChat sends only what you write, one message per tap, and never automates gameplay, but the risk is yours to accept.
- Phone traffic isn't encrypted. Your pairing secret never crosses the network after you scan the code, but someone on the same Wi-Fi could read chat. Use it on networks you trust.
- The phone page's art is original. Fonts are Cinzel, Metamorphous and Marcellus (SIL Open Font License). DeckChat isn't affiliated with Blizzard.

## Development

```sh
make test          # Go and phone unit tests
make test-linux    # uinput, X clipboard, focus and end-to-end tests in Docker
make lint-addon    # luacheck in Docker
make release       # dist/deckchat.tar.gz (linux/amd64)
test/linux/install-smoke.sh   # install/uninstall round trip in an x86-64 container
```

The design and requirements are in `docs/plans/`, the wire protocol is in `docs/protocol.md`, and the on-Deck checks still to run are in `docs/research/deck-acceptance-checklist.md`.

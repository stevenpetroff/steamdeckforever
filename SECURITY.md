# Security

DeckChat runs a small web server on your Steam Deck and can paste text into WoW, so security reports are welcome.

## Reporting a problem

Please report it privately through GitHub: on this repository's **Security** tab, choose **Report a vulnerability**. Don't open a public issue for a security problem.

Include what you found, how to reproduce it, and which release you tested. You'll get a reply as soon as possible. This is a hobby project, so there's no guaranteed response time.

## What's in scope

- Getting the Deck to send or paste text into WoW without the paired phone.
- Reading chat or the pairing secret from another device without pairing.
- Reaching the DeckChat server from outside the local network.
- Anything in `install.sh` that weakens the Deck's security beyond the one udev rule it documents.

Known limits, documented in the README: traffic on your Wi-Fi isn't encrypted, so someone on the same network can read chat (but not send it). And in combat, the paste guard can't block Ctrl+V.

## Supported versions

Only the latest release gets fixes.

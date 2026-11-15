# On-Deck acceptance checklist

The on-device spike (U1) was deferred on 2026-11-14 in favor of Docker-only testing. These checks can't run off-device. Run them on a Steam Deck with WoW Forever, then record the date, SteamOS version and the result next to each item.

## What Docker already proved (2026-11-14, linux/arm64, Xvfb standing in for gamescope)

- A `/dev/uinput` virtual keyboard creates a device and emits exactly chord → Ctrl+V → Enter, with every key-down matched by a key-up.
- The X clipboard owner serves UTF-8 (including emoji). Read-back on a second connection matches before any keys are pressed.
- Focus detection reads `GAMESCOPE_FOCUSED_APP`/`GAMESCOPE_FOCUSED_WINDOW` and the window title.
- End to end: the service finds a fake WoW install, writes `Pairing.lua`, authenticates a WebSocket client, sends history and live chat, and reports `sent` for a pasted message.
- `install.sh` and `uninstall.sh` complete a round trip on x86-64 (systemd and udev are stubbed).

## Stop conditions

If any of these fail, stop and revisit the plan (Goal Capsule).

1. [ ] **Flush latency.** With the add-on loaded, a guild message appears in `WoWChatLog.txt` within flush interval + 1 s. Measure at 2 s, and at 1 s if 2 s feels slow.
2. [ ] **Virtual keyboard reaches WoW.** In Game Mode, `deckchat send "/s test"` posts "test" in Say.
3. [ ] **Paste reaches WoW** (KTD3). Plain text, `héllo 🙂`, and two different messages sent back to back all arrive exactly as written.
4. [ ] **Clean chat box** (KTD4). With chat closed, the chord opens an empty box on the sticky channel. With `hel` part-typed in the box, the chord clears it.

## Confirm assumptions

5. [ ] `deckchat doctor` in Game Mode passes the game display check. Record which display (`:0`/`:1`), the Xauthority path, and the focused window title WoW reports.
6. [ ] `/dev/uinput` writable by `deck` on stock SteamOS, with no udev rule needed? Run `getfacl /dev/uinput`.
7. [ ] `ChatFrameUtil.OpenChat` exists in the Forever client, and `/deckchat status` reports logging on after login.
8. [ ] The binding works after logging in during combat (it's set once combat ends).
9. [ ] `/reload` picks up an edited `Pairing.lua`. A freshly installed add-on needs a full restart.
10. [ ] Instance chat: does party chat inside a dungeon reach the log? If not, wire `instance_paused` (see the plan's Risks).
11. [ ] Capture a real log sample covering every channel type. Anonymize it and replace `companion/internal/chatlog/testdata/forever-sample.log`, then fix any parser mismatches. Also check how emotes appear, since today they parse as system lines.
12. [ ] `## Interface:` number in `DeckChat.toc` matches Forever's (`/dump select(4, GetBuildInfo())`).

## Product acceptance (handheld and docked)

13. [ ] F1: fresh install, reboot into Game Mode, start WoW, `/deckchat pair`, scan. Live chat appears on the phone.
14. [ ] AE1: sticky Guild + `brb` → Guild. `/p pulling` → Party.
15. [ ] AE2: tap a whisper, reply, and it reaches only that player.
16. [ ] AE3: an over-length message is blocked on the phone.
17. [ ] AE4: with the Steam overlay open, Send shows "WoW isn't in front" and keeps the text.
18. [ ] AE5: while holding a movement key, a phone send arrives intact and movement isn't stuck.
19. [ ] AE6: a 10-minute packet capture on the Deck shows no connections leaving the LAN from `deckchat` or the phone page.
20. [ ] Suspend/resume and a Desktop ↔ Game Mode switch: the phone reconnects and sends work without restarting the service.
21. [ ] `deckchat pair --reset` + `/reload` + re-scan: the old phone shows "Not paired", and the new scan works.
22. [ ] Latency: median in-game → phone delay ≤ 2 s over 20 messages (accepted floor 5 s).
23. [ ] Uninstall removes everything, and the game still launches.

## Paste mode

24. [ ] **Paste mode reaches a selected box.** Open the WeakAuras import window, click its box, and paste a real import string (50 KB+) from the phone. The import preview appears. Also `deckchat paste - < aura.txt`.
25. [ ] **Paste guard.** With no text box selected (out of combat), Paste prints the DeckChat hint and Ctrl+V does nothing else. Bind Ctrl+V to something visible first to check.
26. [ ] **Guard chord is swallowed by edit boxes.** With the chat box or an import box focused, the F11 chord inserts nothing and doesn't trigger the guard message.

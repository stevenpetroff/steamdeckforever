# deckchat wire protocol

The phone page and the `deckchat` service talk over one WebSocket at `ws://<deck-lan-ip>:7480/ws`. Every frame is a JSON text message with a `type` field. Go types live in `companion/internal/protocol`.

## Secret

The pairing secret is 16 random bytes, written as 32 lowercase hex characters. The pairing URL is `http://<deck-lan-ip>:7480/#<secret-hex>`. Browsers never send the fragment to the server. After pairing the secret never crosses the network.

HMAC keys are the 16 raw secret bytes (hex-decoded). MACs are lowercase hex HMAC-SHA256.

## Handshake

1. Server → `{"type":"challenge","nonce":"<32 hex chars>"}` immediately after the upgrade. The nonce is single-use, per connection.
2. Client → `{"type":"auth","mac":"<hex>"}` within 5 seconds, where `mac = HMAC(secret, "auth|" + nonce)`.
3. On a bad or missing MAC, the server closes the socket with status 1008 (policy violation) and sends nothing else.
4. On success, the server sends `history`, then `status`, then live frames.

## Server → client

- `{"type":"history","messages":[Message, ...]}`: the full recent buffer, oldest first, at most 500. It's also re-sent after the server drops messages for a slow client. The client replaces its list and de-duplicates by `id`.
- `{"type":"message","message":Message}`: one new chat line.
- `{"type":"send_result","id":"<id>","outcome":"sent"|"not_sent","reason":"<reason>"}`: `reason` is present only for `not_sent`.
- `{"type":"status","status":Status}`: sent on connect and whenever a value changes.

`Message`:

```json
{"id":"1042","time":"11/14 18:02:11.204","channel":"whisper","sender":"Thrall","sender_realm":"Doomhowl","text":"hey","outgoing":false}
```

`channel` is one of `say`, `yell`, `whisper`, `party`, `party_leader`, `raid`, `raid_leader`, `raid_warning`, `guild`, `officer`, `channel`, `emote` or `system`. `channel_name` is set for `channel`, for example `2. Trade - City`. `outgoing` is true for whispers the player sent. Optional fields are omitted when empty.

`Status`:

```json
{"wow_focused":true,"log_fresh":true,"last_log_at":"2026-11-14T18:02:11Z","input_available":true,"instance_paused":false}
```

`reason` is one of:

- `not_focused`: WoW isn't the focused window.
- `focus_lost`: focus moved away mid-send.
- `too_long`: more than 255 UTF-8 bytes.
- `invalid_text`: empty, or contains a newline.
- `input_unavailable`: the virtual keyboard or clipboard is unavailable.
- `stale`: `ts` is more than 10 s old.

## Client → server

`{"type":"send","id":"<client id>","ts":<unix ms>,"text":"<message>","mac":"<hex>"}`

- `id` is 1–64 characters of `[A-Za-z0-9_-]`, unique per message. It's reused when resending after a reconnect.
- `mac = HMAC(secret, nonce + "|" + id + "|" + ts + "|" + text)`, using the current connection's nonce and `ts` as a decimal integer string.
- A bad MAC closes the socket with 1008, and nothing is typed.
- A `ts` more than 10 s off the server clock gets `not_sent` / `stale`.
- A repeated `id` gets the stored result of the first attempt (or waits for it). It's never sent to the game twice.

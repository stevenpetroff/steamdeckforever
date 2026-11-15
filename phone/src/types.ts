// Wire types mirroring docs/protocol.md and companion/internal/protocol.

export type Channel =
  | 'say'
  | 'yell'
  | 'whisper'
  | 'party'
  | 'party_leader'
  | 'raid'
  | 'raid_leader'
  | 'raid_warning'
  | 'guild'
  | 'officer'
  | 'channel'
  | 'emote'
  | 'system';

export interface Message {
  id: string;
  time: string;
  /** One of Channel; kept as string so unknown values from a newer server still render. */
  channel: Channel | (string & {});
  channel_name?: string;
  sender?: string;
  sender_realm?: string;
  text: string;
  outgoing?: boolean;
}

export interface Status {
  wow_focused: boolean;
  log_fresh: boolean;
  last_log_at?: string;
  input_available: boolean;
  instance_paused: boolean;
}

export type NotSentReason =
  | 'not_focused'
  | 'focus_lost'
  | 'too_long'
  | 'invalid_text'
  | 'input_unavailable'
  | 'stale';

export interface SendResult {
  id: string;
  outcome: 'sent' | 'not_sent';
  reason?: NotSentReason | (string & {});
}

export type ServerFrame =
  | { type: 'challenge'; nonce: string }
  | { type: 'history'; messages: Message[] }
  | { type: 'message'; message: Message }
  | ({ type: 'send_result' } & SendResult)
  | { type: 'status'; status: Status };

export const MAX_MESSAGE_BYTES = 255;
/** Paste mode limit (WeakAura strings etc.); matches protocol.MaxPasteBytes. */
export const MAX_PASTE_BYTES = 192 * 1024;

/** chat: one line through a clean chat box. paste: into the focused WoW text box, no Enter. */
export type SendMode = 'chat' | 'paste';
/** Server rejects a send whose ts is more than 10 s old; the phone resends only inside this window. */
export const SEND_WINDOW_MS = 10_000;

// Channel colors and prefix formats, matching WoW's default chat frame.
import type { Channel, Message } from './types';

/** Solid chat panel background (also --chat-bg in theme.css). */
export const CHAT_BG = '#0b0907';
/** Lightest stop of the chat panel's stone gradient (also --chat-bg-light in theme.css). */
export const CHAT_BG_LIGHTEST = '#1c1712';

/** WoW's default chat colors per protocol channel. */
export const WOW_COLORS: Record<Channel, string> = {
  say: '#FFFFFF',
  yell: '#FF4040',
  whisper: '#FF80FF',
  party: '#AAAAFF',
  party_leader: '#77C8FF',
  raid: '#FF7F00',
  raid_leader: '#FF4809',
  raid_warning: '#FF4800',
  guild: '#40FF40',
  officer: '#40C040',
  channel: '#FFC0C0',
  emote: '#FF8040',
  system: '#FFFF00',
};

// Every WoW color above already meets WCAG AA (>= 4.5:1) against both
// CHAT_BG and CHAT_BG_LIGHTEST (the darkest, Raid Warning #FF4800, is ~5.2:1
// on the lightest stop), so no lightened variants are needed. channels.test.ts
// enforces this; if the background changes and a color fails, add a lightened
// override here with a comment rather than changing WOW_COLORS.
const DISPLAY_OVERRIDES: Partial<Record<Channel, string>> = {};

const LABELS: Partial<Record<Channel, string>> = {
  guild: 'Guild',
  officer: 'Officer',
  party: 'Party',
  party_leader: 'Party Leader',
  raid: 'Raid',
  raid_leader: 'Raid Leader',
  raid_warning: 'Raid Warning',
};

export interface ChannelStyle {
  channel: Channel;
  color: string;
  /** CSS class for the line, e.g. ch-guild. */
  cls: string;
  known: boolean;
}

function isChannel(c: string): c is Channel {
  return Object.prototype.hasOwnProperty.call(WOW_COLORS, c);
}

export function channelStyle(channel: string): ChannelStyle {
  const known = isChannel(channel);
  const ch: Channel = known ? channel : 'system';
  return {
    channel: ch,
    color: DISPLAY_OVERRIDES[ch] ?? WOW_COLORS[ch],
    cls: `ch-${ch.replace('_', '-')}`,
    known,
  };
}

export type SegmentRole = 'channel' | 'name' | 'sep' | 'text';
export interface Segment {
  text: string;
  role: SegmentRole;
}

export function displayName(msg: Pick<Message, 'sender' | 'sender_realm'>): string {
  if (!msg.sender) return '';
  return msg.sender_realm ? `${msg.sender}-${msg.sender_realm}` : msg.sender;
}

/** Split a message into the pieces WoW shows: channel tag, [Name], text. */
export function formatSegments(msg: Message): Segment[] {
  const { channel } = channelStyle(msg.channel);
  const known = isChannel(msg.channel);
  const name = displayName(msg);
  const nameSeg: Segment = { text: `[${name}]`, role: 'name' };
  const text: Segment = { text: msg.text, role: 'text' };

  if (!name) return [text];

  if (!known || channel === 'system') {
    return [nameSeg, { text: ': ', role: 'sep' }, text];
  }

  switch (channel) {
    case 'say':
      return [nameSeg, { text: ' says: ', role: 'sep' }, text];
    case 'yell':
      return [nameSeg, { text: ' yells: ', role: 'sep' }, text];
    case 'whisper':
      return msg.outgoing
        ? [{ text: 'To ', role: 'sep' }, nameSeg, { text: ': ', role: 'sep' }, text]
        : [nameSeg, { text: ' whispers: ', role: 'sep' }, text];
    case 'emote':
      // Emotes read as plain text ("Jaina waves."); add the name only when
      // the log line didn't already start with it.
      return msg.text.startsWith(msg.sender!)
        ? [text]
        : [{ text: `${name} `, role: 'sep' }, text];
    case 'channel':
      return [
        { text: `[${msg.channel_name || 'Channel'}] `, role: 'channel' },
        nameSeg,
        { text: ': ', role: 'sep' },
        text,
      ];
    default:
      return [
        { text: `[${LABELS[channel] ?? channel}] `, role: 'channel' },
        nameSeg,
        { text: ': ', role: 'sep' },
        text,
      ];
  }
}

export function formatLine(msg: Message): string {
  return formatSegments(msg)
    .map((s) => s.text)
    .join('');
}

function luminance(hex: string): number {
  const n = parseInt(hex.replace('#', ''), 16);
  const chan = (v: number) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * chan((n >> 16) & 255) + 0.7152 * chan((n >> 8) & 255) + 0.0722 * chan(n & 255);
}

/** WCAG 2.x contrast ratio between two #RRGGBB colors. */
export function contrastRatio(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

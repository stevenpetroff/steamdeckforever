import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  CHAT_BG,
  CHAT_BG_LIGHTEST,
  WOW_COLORS,
  channelStyle,
  contrastRatio,
  formatLine,
  formatSegments,
} from './channels';
import type { Message } from './types';

const m = (over: Partial<Message>): Message => ({
  id: '1',
  time: '11/14 18:02:11.204',
  channel: 'say',
  text: 'hello',
  ...over,
});

describe('channel colors', () => {
  const expected: Record<string, string> = {
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

  it('uses the WoW chat color for every protocol channel', () => {
    for (const [ch, color] of Object.entries(expected)) {
      expect(WOW_COLORS[ch as keyof typeof WOW_COLORS]).toBe(color);
      expect(channelStyle(ch).known).toBe(true);
    }
  });

  it('every displayed color meets WCAG AA (4.5:1) on the lightest chat background', () => {
    for (const ch of Object.keys(expected)) {
      const { color } = channelStyle(ch);
      expect(contrastRatio(color, CHAT_BG_LIGHTEST), ch).toBeGreaterThanOrEqual(4.5);
      expect(contrastRatio(color, CHAT_BG), ch).toBeGreaterThanOrEqual(4.5);
    }
  });

  it('unknown channels fall back to system styling', () => {
    const s = channelStyle('battleground');
    expect(s.known).toBe(false);
    expect(s.color).toBe(channelStyle('system').color);
    expect(s.cls).toBe('ch-system');
  });

  it('theme.css uses the same chat background colors', () => {
    const css = readFileSync(resolve(process.cwd(), 'src/theme.css'), 'utf8').toLowerCase();
    expect(css).toContain(`--chat-bg: ${CHAT_BG.toLowerCase()}`);
    expect(css).toContain(`--chat-bg-light: ${CHAT_BG_LIGHTEST.toLowerCase()}`);
  });
});

describe('prefix formats', () => {
  const cases: Array<[Partial<Message>, string]> = [
    [{ channel: 'guild', sender: 'Jaina', text: 'hi' }, '[Guild] [Jaina]: hi'],
    [{ channel: 'officer', sender: 'Jaina', text: 'hi' }, '[Officer] [Jaina]: hi'],
    [{ channel: 'party', sender: 'Jaina', text: 'hi' }, '[Party] [Jaina]: hi'],
    [{ channel: 'party_leader', sender: 'Jaina', text: 'hi' }, '[Party Leader] [Jaina]: hi'],
    [{ channel: 'raid', sender: 'Jaina', text: 'hi' }, '[Raid] [Jaina]: hi'],
    [{ channel: 'raid_leader', sender: 'Jaina', text: 'hi' }, '[Raid Leader] [Jaina]: hi'],
    [{ channel: 'raid_warning', sender: 'Jaina', text: 'hi' }, '[Raid Warning] [Jaina]: hi'],
    [
      { channel: 'channel', channel_name: '2. Trade - City', sender: 'Jaina', text: 'WTS' },
      '[2. Trade - City] [Jaina]: WTS',
    ],
    [{ channel: 'whisper', sender: 'Thrall', sender_realm: 'Doomhowl', text: 'hey' }, '[Thrall-Doomhowl] whispers: hey'],
    [{ channel: 'whisper', sender: 'Thrall', text: 'hey', outgoing: true }, 'To [Thrall]: hey'],
    [{ channel: 'say', sender: 'Jaina', text: 'hi' }, '[Jaina] says: hi'],
    [{ channel: 'yell', sender: 'Jaina', text: 'hi' }, '[Jaina] yells: hi'],
    [{ channel: 'emote', sender: 'Jaina', text: 'Jaina waves.' }, 'Jaina waves.'],
    [{ channel: 'system', text: 'Thrall has come online.' }, 'Thrall has come online.'],
    [{ channel: 'mystery', text: 'odd line' }, 'odd line'],
  ];
  for (const [over, want] of cases) {
    it(`${over.channel}${over.outgoing ? ' (outgoing)' : ''} → ${want}`, () => {
      expect(formatLine(m(over))).toBe(want);
    });
  }

  it('marks the sender segment so it can be styled as a name', () => {
    const segs = formatSegments(m({ channel: 'guild', sender: 'Jaina', text: 'hi' }));
    expect(segs.find((s) => s.role === 'name')?.text).toBe('[Jaina]');
  });
});

describe('contrastRatio', () => {
  it('black on white is 21:1', () => {
    expect(contrastRatio('#000000', '#FFFFFF')).toBeCloseTo(21, 1);
  });
});

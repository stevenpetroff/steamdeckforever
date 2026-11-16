import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ChatView } from './chat';
import { Compose } from './compose';
import type { Message } from './types';

const LINE = 20;
const VIEW = 100;

/** jsdom has no layout: fake a scroll container where each line is 20px. */
function fakeLayout(el: HTMLElement) {
  let top = 0;
  const height = () => el.querySelectorAll('.msg').length * LINE;
  Object.defineProperty(el, 'scrollHeight', { configurable: true, get: height });
  Object.defineProperty(el, 'clientHeight', { configurable: true, get: () => VIEW });
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get: () => top,
    set: (v: number) => {
      top = Math.max(0, Math.min(v, Math.max(0, height() - VIEW)));
    },
  });
  return {
    scrollUserTo(v: number) {
      el.scrollTop = v;
      el.dispatchEvent(new Event('scroll'));
    },
    get top() {
      return top;
    },
    get max() {
      return Math.max(0, height() - VIEW);
    },
  };
}

let n = 0;
const msg = (over: Partial<Message> = {}): Message => ({
  id: String(++n),
  time: '11/14 18:02:11.204',
  channel: 'say',
  sender: 'Jaina',
  text: 'hello',
  ...over,
});

function setup() {
  document.body.replaceChildren();
  const chatHost = document.createElement('div');
  const composeHost = document.createElement('div');
  document.body.append(chatHost, composeHost);
  const compose = new Compose(composeHost, { send: () => 'id', cancel: () => {} });
  const onTap = vi.fn((m: Message) => {
    if (m.sender) compose.replyTo(m.sender, m.sender_realm);
  });
  const chat = new ChatView(chatHost, { onTap });
  const layout = fakeLayout(chat.list);
  return { chat, compose, onTap, layout };
}

function tap(target: Element, move = 0) {
  target.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, clientX: 10, clientY: 10 }));
  if (move) {
    target.dispatchEvent(
      new MouseEvent('pointermove', { bubbles: true, clientX: 10, clientY: 10 + move }),
    );
  }
  target.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, clientX: 10, clientY: 10 + move }));
  target.dispatchEvent(new MouseEvent('click', { bubbles: true, clientX: 10, clientY: 10 + move }));
}

beforeEach(() => {
  n = 0;
});

describe('rendering', () => {
  it('is an aria-live polite region with newest at the bottom', () => {
    const { chat } = setup();
    chat.add(msg({ text: 'first' }));
    chat.add(msg({ text: 'second' }));
    expect(chat.list.getAttribute('aria-live')).toBe('polite');
    const items = chat.list.querySelectorAll('.msg');
    expect(items[0].textContent).toContain('first');
    expect(items[1].textContent).toContain('second');
  });

  it('renders every field as literal text, never markup', () => {
    const { chat } = setup();
    const evil = '<img src=x onerror=alert(1)><b>x</b>';
    chat.add(
      msg({ channel: 'channel', channel_name: evil, sender: evil, sender_realm: evil, text: evil, time: evil }),
    );
    chat.add(msg({ channel: 'system', sender: undefined, text: evil }));
    expect(chat.list.querySelector('img, b, script')).toBeNull();
    const line = chat.list.querySelector('.msg-text')!;
    expect(line.textContent).toBe(evil);
    expect(line.children).toHaveLength(0);
    expect(chat.list.querySelectorAll('.msg')[1].textContent).toContain(evil);
  });

  it('applies the channel color and marks player lines as tappable', () => {
    const { chat } = setup();
    chat.add(msg({ channel: 'guild' }));
    chat.add(msg({ channel: 'system', sender: undefined, text: 'Server restart' }));
    const [guild, system] = chat.list.querySelectorAll<HTMLElement>('.msg');
    expect(guild.style.getPropertyValue('--ch')).toBe('#40FF40');
    expect(guild.classList.contains('tappable')).toBe(true);
    expect(system.classList.contains('tappable')).toBe(false);
  });
});

describe('tap to reply', () => {
  it('Covers AE2. tapping a whisper from Thrall-Doomhowl prefills /w Thrall-Doomhowl with caret at end', () => {
    const { chat, compose } = setup();
    chat.add(msg({ channel: 'whisper', sender: 'Thrall', sender_realm: 'Doomhowl', text: 'need help?' }));
    tap(chat.list.querySelector('.msg-text')!);
    expect(compose.text).toBe('/w Thrall-Doomhowl ');
    expect(compose.textarea.selectionStart).toBe(compose.text.length);
  });

  it('tapping a system message does nothing', () => {
    const { chat, compose, onTap } = setup();
    chat.add(msg({ channel: 'system', sender: undefined, text: 'Thrall has come online.' }));
    tap(chat.list.querySelector('.msg')!);
    expect(onTap).not.toHaveBeenCalled();
    expect(compose.text).toBe('');
  });

  it('a tap that moved more than 10px (a scroll) does not count', () => {
    const { chat, onTap } = setup();
    chat.add(msg({ channel: 'whisper', sender: 'Thrall' }));
    tap(chat.list.querySelector('.msg')!, 25);
    expect(onTap).not.toHaveBeenCalled();
    tap(chat.list.querySelector('.msg')!, 6);
    expect(onTap).toHaveBeenCalledTimes(1);
  });

  it('with a draft, shows the replace prompt and declining keeps the draft', () => {
    const { chat, compose } = setup();
    compose.text = 'on my way';
    chat.add(msg({ channel: 'whisper', sender: 'Thrall', sender_realm: 'Doomhowl' }));
    tap(chat.list.querySelector('.msg')!);
    expect(compose.confirmBox.hidden).toBe(false);
    expect(compose.confirmText.textContent).toBe('Replace draft with reply to Thrall?');
    compose.confirmNo.click();
    expect(compose.text).toBe('on my way');
  });
});

describe('scrolling', () => {
  it('auto-scrolls when at the bottom', () => {
    const { chat, layout } = setup();
    for (let i = 0; i < 10; i++) chat.add(msg());
    expect(layout.top).toBe(layout.max);
    expect(chat.pill.hidden).toBe(true);
  });

  it('when scrolled up, new messages show the pill without moving the scroll position', () => {
    const { chat, layout } = setup();
    for (let i = 0; i < 10; i++) chat.add(msg());
    layout.scrollUserTo(20);
    chat.add(msg());
    chat.add(msg());
    expect(layout.top).toBe(20);
    expect(chat.pill.hidden).toBe(false);
    expect(chat.pill.textContent).toBe('2 new messages');
    chat.add(msg());
    expect(chat.pill.textContent).toBe('3 new messages');
    chat.pill.click();
    expect(layout.top).toBe(layout.max);
    expect(chat.pill.hidden).toBe(true);
  });

  it('pill says "1 new message" in the singular and hides when the user scrolls back down', () => {
    const { chat, layout } = setup();
    for (let i = 0; i < 10; i++) chat.add(msg());
    layout.scrollUserTo(0);
    chat.add(msg());
    expect(chat.pill.textContent).toBe('1 new message');
    layout.scrollUserTo(layout.max);
    expect(chat.pill.hidden).toBe(true);
  });
});

describe('history', () => {
  it('replaces the list, de-duplicated by id, and keeps the scroll position', () => {
    const { chat, layout } = setup();
    const first = Array.from({ length: 10 }, () => msg());
    first.forEach((m) => chat.add(m));
    layout.scrollUserTo(40);
    const extra = msg({ text: 'new after reconnect' });
    chat.setHistory([...first, first[3], extra]);
    const ids = [...chat.list.querySelectorAll<HTMLElement>('.msg')].map((e) => e.dataset.id);
    expect(ids).toEqual([...first.map((m) => m.id), extra.id]);
    expect(layout.top).toBe(40);
    expect(chat.pill.textContent).toBe('1 new message');
  });

  it('a live message already in the list is not duplicated', () => {
    const { chat } = setup();
    const m = msg();
    chat.setHistory([m]);
    chat.add(m);
    expect(chat.list.querySelectorAll('.msg')).toHaveLength(1);
  });

  it('stays pinned to the bottom when it was at the bottom', () => {
    const { chat, layout } = setup();
    chat.setHistory(Array.from({ length: 12 }, () => msg()));
    expect(layout.top).toBe(layout.max);
  });
});

describe('viewport resize', () => {
  it('keepBottom re-pins only when the view was at the bottom before the resize', () => {
    const { chat, layout } = setup();
    for (let i = 0; i < 10; i++) chat.add(msg());
    // Simulate the list shrinking (keyboard opened) leaving it short of the bottom.
    chat.list.scrollTop = 50;
    chat.keepBottom();
    expect(layout.top).toBe(layout.max);
    layout.scrollUserTo(10);
    chat.keepBottom();
    expect(layout.top).toBe(10);
  });
});

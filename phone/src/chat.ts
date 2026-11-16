// Chat list: renders messages newest-at-bottom with WoW colors and prefixes.
//
// Every field is rendered with createElement + textContent. Never use
// innerHTML or insertAdjacentHTML here: chat text comes from other players.
import { channelStyle, displayName, formatSegments } from './channels';
import type { Message } from './types';

/** Distance from the bottom (px) that still counts as "at the bottom". */
const BOTTOM_SLACK = 24;
/** Pointer travel (px) after which a touch is a scroll, not a tap. */
const TAP_SLOP = 10;
/** Oldest lines are dropped beyond this many. */
const MAX_LINES = 1000;

export interface ChatViewOptions {
  /** Called when the player taps a player-authored message. */
  onTap(message: Message): void;
}

function shortTime(time: string): string | null {
  const m = /\b(\d{1,2}:\d{2}):\d{2}/.exec(time);
  return m ? m[1] : null;
}

export class ChatView {
  readonly root: HTMLElement;
  readonly list: HTMLOListElement;
  readonly pill: HTMLButtonElement;
  private readonly byId = new Map<string, { msg: Message; el: HTMLLIElement }>();
  private unseen = 0;
  /** Whether the view was at the bottom as of the last scroll/append (survives a resize). */
  pinned = true;
  private pointer: { x: number; y: number; moved: boolean } | null = null;

  constructor(
    host: HTMLElement,
    private readonly opts: ChatViewOptions,
  ) {
    this.root = document.createElement('div');
    this.root.className = 'chat';

    this.list = document.createElement('ol');
    this.list.className = 'chat-list';
    this.list.setAttribute('role', 'log');
    this.list.setAttribute('aria-live', 'polite');
    this.list.setAttribute('aria-relevant', 'additions');
    this.list.setAttribute('aria-label', 'Chat');

    this.pill = document.createElement('button');
    this.pill.type = 'button';
    this.pill.className = 'chat-pill tap';
    this.pill.hidden = true;

    this.root.append(this.list, this.pill);
    host.append(this.root);

    this.list.addEventListener('scroll', () => {
      this.pinned = this.isAtBottom();
      if (this.pinned) this.clearUnseen();
    });
    this.pill.addEventListener('click', () => this.scrollToBottom());

    this.list.addEventListener('pointerdown', (ev) => {
      const e = ev as PointerEvent;
      this.pointer = { x: e.clientX, y: e.clientY, moved: false };
    });
    this.list.addEventListener('pointermove', (ev) => {
      const e = ev as PointerEvent;
      const p = this.pointer;
      if (p && Math.hypot(e.clientX - p.x, e.clientY - p.y) > TAP_SLOP) p.moved = true;
    });
    this.list.addEventListener('pointercancel', () => {
      // The browser took the gesture over for scrolling.
      if (this.pointer) this.pointer.moved = true;
    });
    this.list.addEventListener('click', (ev) => {
      const moved = this.pointer?.moved ?? false;
      this.pointer = null;
      if (moved) return;
      this.activate(ev.target);
    });
    this.list.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter' || ev.key === ' ') {
        if (this.activate(ev.target)) ev.preventDefault();
      }
    });
  }

  isAtBottom(): boolean {
    const l = this.list;
    return l.scrollHeight - l.scrollTop - l.clientHeight <= BOTTOM_SLACK;
  }

  scrollToBottom(): void {
    this.list.scrollTop = this.list.scrollHeight;
    this.pinned = true;
    this.clearUnseen();
  }

  /** Append one live message (ignored if its id is already shown). */
  add(msg: Message): void {
    if (this.byId.has(msg.id)) return;
    const atBottom = this.isAtBottom();
    this.list.append(this.render(msg));
    this.trim();
    if (atBottom) {
      this.scrollToBottom();
    } else {
      this.bumpUnseen(1);
    }
  }

  /** Replace the list with a history frame, de-duplicated by id, keeping the scroll position. */
  setHistory(messages: Message[]): void {
    const atBottom = this.isAtBottom();
    const top = this.list.scrollTop;
    const hadAny = this.byId.size > 0;
    const previous = new Set(this.byId.keys());
    this.byId.clear();
    const items: HTMLLIElement[] = [];
    let fresh = 0;
    for (const m of messages) {
      if (!m || typeof m.id !== 'string' || this.byId.has(m.id)) continue;
      if (!previous.has(m.id)) fresh++;
      items.push(this.render(m));
    }
    this.list.replaceChildren(...items);
    this.trim();
    if (atBottom) {
      this.scrollToBottom();
    } else {
      this.list.scrollTop = top;
      if (hadAny && fresh > 0) this.bumpUnseen(fresh);
    }
  }

  /**
   * Keep the newest message visible after a viewport resize (keyboard
   * opening): if the view was pinned before the resize, re-pin it.
   */
  keepBottom(): void {
    if (this.pinned) this.scrollToBottom();
  }

  private activate(target: EventTarget | null): boolean {
    if (!(target instanceof Element)) return false;
    const li = target.closest<HTMLLIElement>('li.msg');
    if (!li || !this.list.contains(li)) return false;
    const entry = this.byId.get(li.dataset.id ?? '');
    if (!entry || !entry.msg.sender) return false;
    this.opts.onTap(entry.msg);
    return true;
  }

  private bumpUnseen(n: number): void {
    this.unseen += n;
    this.pill.textContent = `${this.unseen} new ${this.unseen === 1 ? 'message' : 'messages'}`;
    this.pill.hidden = false;
  }

  private clearUnseen(): void {
    this.unseen = 0;
    this.pill.hidden = true;
  }

  private trim(): void {
    while (this.list.children.length > MAX_LINES) {
      const first = this.list.firstElementChild as HTMLLIElement;
      this.byId.delete(first.dataset.id ?? '');
      first.remove();
    }
  }

  private render(msg: Message): HTMLLIElement {
    const style = channelStyle(msg.channel);
    const li = document.createElement('li');
    li.className = `msg ${style.cls}`;
    if (msg.outgoing) li.classList.add('outgoing');
    li.dataset.id = msg.id;
    li.style.setProperty('--ch', style.color);

    const time = shortTime(msg.time ?? '');
    if (time) {
      const t = document.createElement('span');
      t.className = 'msg-time';
      t.textContent = time;
      li.append(t);
    }

    const body = document.createElement('span');
    body.className = 'msg-body';
    for (const seg of formatSegments(msg)) {
      const s = document.createElement('span');
      s.className = `msg-${seg.role}`;
      s.textContent = seg.text;
      body.append(s);
    }
    li.append(body);

    if (msg.sender) {
      li.classList.add('tappable');
      li.tabIndex = 0;
      li.title = `Tap to reply to ${displayName(msg)}`;
    }

    this.byId.set(msg.id, { msg, el: li });
    return li;
  }
}

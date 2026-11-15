// Compose bar: textarea, live UTF-8 byte counter, Send/Cancel and send state.
//
// States: composing → sending → sent (box cleared) | not_sent(reason) (text
// kept). sending times out after 10 s into not_sent(no_connection).
//
// Two modes: chat (one line into WoW's chat box, 255 bytes) and paste (any
// text, newlines kept, into whichever WoW text box is selected, e.g. a
// WeakAura import). Each mode keeps its own draft.
import { WOW_COLORS } from './channels';
import { utf8Length } from './hmac';
import {
  MAX_MESSAGE_BYTES,
  MAX_PASTE_BYTES,
  SEND_WINDOW_MS,
  type Channel,
  type SendMode,
  type SendResult,
} from './types';

export type ComposeState = 'composing' | 'sending' | 'sent' | 'not_sent';

export interface ComposeDeps {
  /** Queue text for sending; returns the send id, or null if it can't be sent at all. */
  send(text: string, mode: SendMode): string | null;
  /** Stop tracking a send (no resend, result ignored). */
  cancel(id: string): void;
}

/** Plain-language copy for each not_sent reason (KTD7) plus the phone's own no_connection. */
export const REASON_COPY: Record<string, string> = {
  not_focused: "WoW isn't in front — tap back into the game",
  focus_lost: 'WoW lost focus mid-send — try again',
  too_long: 'Message too long — shorten it',
  invalid_text: "Can't send empty text or line breaks",
  input_unavailable: 'Deck keyboard unavailable — re-run the installer',
  stale: 'Took too long — try again',
  no_connection: 'Not connected to your Deck',
};

const NEWLINES = new RegExp('\\r\\n|[\\r\\n\\u2028\\u2029\\u0085]', 'g');
/** A reply prefill with nothing typed after it, e.g. "/w Thrall-Doomhowl ". */
const PREFILL_ONLY = /^\/w \S+ ?$/;
/** A leading WoW chat command that picks the channel, e.g. "/g " or "/w Name ". */
const CHANNEL_PREFIX =
  /^\/(?:(?:w|whisper|t|tell)\s+\S+|g|guild|p|party|ra|raid|r|reply|s|say|y|yell|o|officer|i|instance)(?:\s+|$)/i;

interface Shortcut {
  key: 'guild' | 'party' | 'raid' | 'reply';
  label: string;
  channel: Channel;
}
/** Shortcut chips above the box, each in its WoW channel color. */
const SHORTCUTS: Shortcut[] = [
  { key: 'guild', label: 'Guild', channel: 'guild' },
  { key: 'party', label: 'Party', channel: 'party' },
  { key: 'raid', label: 'Raid', channel: 'raid' },
  { key: 'reply', label: 'Reply', channel: 'whisper' },
];
const FIXED_PREFIX: Record<Exclude<Shortcut['key'], 'reply'>, string> = {
  guild: '/g ',
  party: '/p ',
  raid: '/ra ',
};

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  cls: string,
  text?: string,
): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/** Byte counts as people read them: "812 B", "14.2 KB". */
export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  return `${kb < 10 ? kb.toFixed(1) : Math.round(kb)} KB`;
}

function tooLongCopy(over: number, mode: SendMode): string {
  if (mode === 'paste') return `Too long — remove ${formatSize(over)}`;
  return `Too long — remove ${over} ${over === 1 ? 'byte' : 'bytes'}`;
}

const PLACEHOLDER: Record<SendMode, string> = {
  chat: 'Say something…',
  paste: 'Paste a WeakAura string or any text. In WoW, click the box it goes in first.',
};

export class Compose {
  readonly root: HTMLElement;
  readonly textarea: HTMLTextAreaElement;
  readonly sendButton: HTMLButtonElement;
  readonly cancelButton: HTMLButtonElement;
  readonly counter: HTMLElement;
  readonly warning: HTMLElement;
  readonly notice: HTMLElement;
  readonly confirmBox: HTMLElement;
  readonly confirmText: HTMLElement;
  readonly confirmYes: HTMLButtonElement;
  readonly confirmNo: HTMLButtonElement;
  readonly shortcuts: Record<Shortcut['key'], HTMLButtonElement>;
  readonly modeButtons: Record<SendMode, HTMLButtonElement>;
  readonly clearButton: HTMLButtonElement;
  private readonly shortcutRow: HTMLElement;

  state: ComposeState = 'composing';
  mode: SendMode = 'chat';
  private readonly drafts: Record<SendMode, string> = { chat: '', paste: '' };
  reason: string | null = null;
  private pendingId: string | null = null;
  private timeout: ReturnType<typeof setTimeout> | null = null;
  private pendingReply: string | null = null;
  /** Last player who whispered us, as "Name" or "Name-Realm". */
  private replyTarget: string | null = null;

  constructor(
    host: HTMLElement,
    private readonly deps: ComposeDeps,
  ) {
    this.root = el('div', 'compose');

    this.confirmBox = el('div', 'compose-confirm');
    this.confirmBox.hidden = true;
    this.confirmBox.setAttribute('role', 'alertdialog');
    this.confirmText = el('span', 'compose-confirm-text');
    this.confirmYes = el('button', 'btn btn-small tap', 'Yes');
    this.confirmNo = el('button', 'btn btn-small btn-quiet tap', 'No');
    for (const b of [this.confirmYes, this.confirmNo]) b.type = 'button';
    const confirmButtons = el('span', 'compose-confirm-buttons');
    confirmButtons.append(this.confirmYes, this.confirmNo);
    this.confirmBox.append(this.confirmText, confirmButtons);

    this.notice = el('div', 'compose-notice');
    this.notice.setAttribute('role', 'status');
    this.notice.setAttribute('aria-live', 'polite');
    this.notice.hidden = true;

    const shortcutRow = el('div', 'compose-shortcuts');
    shortcutRow.setAttribute('role', 'group');
    shortcutRow.setAttribute('aria-label', 'Channel shortcuts');
    const chips = {} as Record<Shortcut['key'], HTMLButtonElement>;
    for (const sc of SHORTCUTS) {
      const b = el('button', 'chip', sc.label);
      b.type = 'button';
      b.dataset.key = sc.key;
      b.style.setProperty('--chip', WOW_COLORS[sc.channel]);
      // Keep the keyboard up: don't move focus off the box on tap.
      b.addEventListener('pointerdown', (ev) => ev.preventDefault());
      b.addEventListener('click', () => this.useShortcut(sc.key));
      chips[sc.key] = b;
      shortcutRow.append(b);
    }
    this.shortcuts = chips;
    this.shortcutRow = shortcutRow;

    const row = el('div', 'compose-row');
    this.textarea = el('textarea', 'compose-input');
    this.textarea.rows = 2;
    this.textarea.placeholder = PLACEHOLDER.chat;
    this.textarea.setAttribute('aria-label', 'Message');
    this.textarea.setAttribute('enterkeyhint', 'done');
    this.textarea.autocapitalize = 'sentences';
    this.textarea.spellcheck = true;

    const actions = el('div', 'compose-actions');
    this.sendButton = el('button', 'btn btn-send tap', 'Send');
    this.sendButton.type = 'button';
    this.cancelButton = el('button', 'btn btn-quiet tap', 'Cancel');
    this.cancelButton.type = 'button';
    this.cancelButton.hidden = true;
    this.clearButton = el('button', 'btn btn-quiet btn-small tap', 'Clear');
    this.clearButton.type = 'button';
    this.clearButton.hidden = true;
    actions.append(this.sendButton, this.cancelButton, this.clearButton);
    row.append(this.textarea, actions);

    const modes = el('div', 'compose-modes');
    modes.setAttribute('role', 'group');
    modes.setAttribute('aria-label', 'Mode');
    const modeButtons = {} as Record<SendMode, HTMLButtonElement>;
    for (const [m, label] of [
      ['chat', 'Chat'],
      ['paste', 'Paste'],
    ] as const) {
      const b = el('button', 'mode', label);
      b.type = 'button';
      b.addEventListener('pointerdown', (ev) => ev.preventDefault());
      b.addEventListener('click', () => this.setMode(m));
      modeButtons[m] = b;
      modes.append(b);
    }
    this.modeButtons = modeButtons;

    const meta = el('div', 'compose-meta');
    this.warning = el('span', 'compose-warning');
    this.warning.hidden = true;
    this.counter = el('span', 'compose-counter');
    this.counter.setAttribute('aria-live', 'off');
    meta.append(modes, this.warning, this.counter);

    this.root.append(this.confirmBox, this.notice, shortcutRow, row, meta);
    host.append(this.root);

    this.textarea.addEventListener('keydown', (ev) => {
      // In chat, Enter never inserts a newline and never sends: only the
      // button sends. Paste mode keeps newlines.
      if (ev.key === 'Enter' && this.mode === 'chat') ev.preventDefault();
    });
    this.textarea.addEventListener('input', () => this.onInput());
    this.sendButton.addEventListener('click', () => this.submit());
    this.cancelButton.addEventListener('click', () => this.cancelSend());
    this.clearButton.addEventListener('click', () => {
      this.textarea.value = '';
      this.toComposing();
      this.textarea.focus();
    });
    this.confirmYes.addEventListener('click', () => {
      const reply = this.pendingReply;
      this.hideConfirm();
      if (reply !== null) this.applyPrefill(reply);
    });
    this.confirmNo.addEventListener('click', () => {
      this.hideConfirm();
      this.textarea.focus();
    });

    this.refresh();
  }

  get text(): string {
    return this.textarea.value;
  }

  set text(value: string) {
    this.textarea.value = this.mode === 'chat' ? value.replace(NEWLINES, ' ') : value;
    this.refresh();
  }

  /** Bytes currently in the box, as WoW counts them. */
  get bytes(): number {
    return utf8Length(this.textarea.value);
  }

  private get limit(): number {
    return this.mode === 'paste' ? MAX_PASTE_BYTES : MAX_MESSAGE_BYTES;
  }

  /** Switch between chat and paste. Each mode keeps its own draft. */
  setMode(mode: SendMode): void {
    if (mode === this.mode || this.state === 'sending') return;
    this.drafts[this.mode] = this.textarea.value;
    this.mode = mode;
    this.textarea.value = this.drafts[mode];
    const paste = mode === 'paste';
    this.textarea.placeholder = PLACEHOLDER[mode];
    this.textarea.rows = paste ? 5 : 2;
    // WeakAura strings must not be autocorrected or capitalized.
    this.textarea.autocapitalize = paste ? 'off' : 'sentences';
    this.textarea.spellcheck = !paste;
    this.textarea.setAttribute('autocorrect', paste ? 'off' : 'on');
    this.textarea.setAttribute('enterkeyhint', paste ? 'enter' : 'done');
    this.shortcutRow.hidden = paste;
    this.hideConfirm();
    this.toComposing();
  }

  /**
   * Prefill a whisper reply to a player (R5). Replaces the box only when it's
   * empty or holds just a /w prefill; otherwise asks first.
   */
  replyTo(sender: string, realm?: string): void {
    if (this.state === 'sending') return;
    this.setMode('chat');
    const target = realm ? `${sender}-${realm}` : sender;
    const prefill = `/w ${target} `;
    const current = this.textarea.value;
    if (current.trim() === '' || PREFILL_ONLY.test(current)) {
      this.hideConfirm();
      this.applyPrefill(prefill);
      return;
    }
    this.pendingReply = prefill;
    this.confirmText.textContent = `Replace draft with reply to ${sender}?`;
    this.confirmBox.hidden = false;
  }

  /** Remember the last player who whispered us, for the Reply shortcut. */
  setReplyTarget(sender: string, realm?: string): void {
    this.replyTarget = realm ? `${sender}-${realm}` : sender;
    this.refresh();
  }

  /**
   * Switch the message's channel: replace any leading channel command with
   * the shortcut's, keeping what's been typed after it. Tapping the active
   * shortcut again removes its prefix.
   */
  useShortcut(key: Shortcut['key']): void {
    if (this.state === 'sending' || this.mode !== 'chat') return;
    const prefix = this.shortcutPrefix(key);
    if (prefix === null) return;
    this.hideConfirm();
    const current = this.textarea.value;
    const body = current.replace(CHANNEL_PREFIX, '');
    const active = this.activeShortcut() === key;
    this.applyPrefill(active ? body : prefix + body);
  }

  private shortcutPrefix(key: Shortcut['key']): string | null {
    if (key === 'reply') return this.replyTarget ? `/w ${this.replyTarget} ` : null;
    return FIXED_PREFIX[key];
  }

  /** The shortcut whose prefix the message currently starts with, if any. */
  private activeShortcut(): Shortcut['key'] | null {
    const m = CHANNEL_PREFIX.exec(this.textarea.value);
    if (!m) return null;
    const cmd = m[0].trim().toLowerCase();
    if (cmd === '/g' || cmd === '/guild') return 'guild';
    if (cmd === '/p' || cmd === '/party') return 'party';
    if (cmd === '/ra' || cmd === '/raid') return 'raid';
    if (this.replyTarget && cmd === `/w ${this.replyTarget}`.toLowerCase()) return 'reply';
    return null;
  }

  /** Apply a send_result from the socket. */
  handleResult(result: SendResult): void {
    if (this.state !== 'sending' || result.id !== this.pendingId) return;
    this.finishPending();
    if (result.outcome === 'sent') {
      this.state = 'sent';
      this.reason = null;
      if (this.mode === 'paste') {
        // Keep the text: if no box was selected in WoW, the paste went
        // nowhere and the player will want to try again.
        this.showNotice('Pasted into WoW. Nothing showed up? Click the box in WoW, then tap Paste again.', 'ok');
      } else {
        this.textarea.value = '';
        this.showNotice('Sent', 'ok');
      }
    } else {
      this.fail(result.reason ?? 'stale');
    }
    this.refresh();
  }

  private applyPrefill(prefill: string): void {
    this.text = prefill;
    if (this.state !== 'sending') this.toComposing();
    this.textarea.focus();
    const end = this.textarea.value.length;
    this.textarea.setSelectionRange(end, end);
  }

  private hideConfirm(): void {
    this.confirmBox.hidden = true;
    this.pendingReply = null;
  }

  private onInput(): void {
    const clean = this.mode === 'chat' ? this.textarea.value.replace(NEWLINES, ' ') : this.textarea.value;
    if (clean !== this.textarea.value) {
      const pos = this.textarea.selectionStart ?? clean.length;
      this.textarea.value = clean;
      this.textarea.setSelectionRange(pos, pos);
    }
    if (this.state === 'sent' || this.state === 'not_sent') this.toComposing();
    this.refresh();
  }

  private toComposing(): void {
    this.state = 'composing';
    this.reason = null;
    this.notice.hidden = true;
    this.notice.textContent = '';
    this.refresh();
  }

  private canSend(): boolean {
    const t = this.textarea.value;
    if (this.state === 'sending' || utf8Length(t) > this.limit) return false;
    if (this.mode === 'paste') return t.trim() !== '';
    // A bare channel prefix ("/g ") would leave WoW's chat box open in game.
    return t.replace(CHANNEL_PREFIX, '').trim() !== '';
  }

  private submit(): void {
    if (!this.canSend()) return;
    this.hideConfirm();
    const text = this.textarea.value;
    const id = this.deps.send(text, this.mode);
    if (id === null) {
      this.fail('no_connection');
      this.refresh();
      return;
    }
    this.pendingId = id;
    this.state = 'sending';
    this.reason = null;
    this.showNotice(this.mode === 'paste' ? 'Pasting into WoW…' : 'Sending to WoW…', 'pending');
    this.timeout = setTimeout(() => {
      const pending = this.pendingId;
      this.finishPending();
      if (pending) this.deps.cancel(pending);
      this.fail('no_connection');
      this.refresh();
    }, SEND_WINDOW_MS);
    this.refresh();
  }

  private cancelSend(): void {
    if (this.state !== 'sending') return;
    const pending = this.pendingId;
    this.finishPending();
    if (pending) this.deps.cancel(pending);
    this.toComposing();
    this.showNotice('Stopped waiting. If the Deck already got it, it will show in chat.', 'info');
    this.textarea.focus();
  }

  private finishPending(): void {
    if (this.timeout !== null) clearTimeout(this.timeout);
    this.timeout = null;
    this.pendingId = null;
  }

  private fail(reason: string): void {
    this.state = 'not_sent';
    this.reason = reason;
    let copy = REASON_COPY[reason] ?? 'Something went wrong — try again';
    if (reason === 'too_long') {
      const over = this.bytes - this.limit;
      if (over > 0) copy = tooLongCopy(over, this.mode);
    }
    this.showNotice(`Not sent — ${copy}`, 'error');
  }

  private showNotice(text: string, kind: 'ok' | 'error' | 'pending' | 'info'): void {
    this.notice.textContent = text;
    this.notice.dataset.kind = kind;
    this.notice.hidden = false;
  }

  private refresh(): void {
    const bytes = this.bytes;
    const over = bytes - this.limit;
    const paste = this.mode === 'paste';
    this.counter.textContent = paste
      ? `${formatSize(bytes)} / ${formatSize(MAX_PASTE_BYTES)}`
      : `${bytes}/${MAX_MESSAGE_BYTES}`;
    this.counter.classList.toggle('over', over > 0);
    this.warning.hidden = over <= 0;
    this.warning.textContent = over > 0 ? tooLongCopy(over, this.mode) : '';
    const sending = this.state === 'sending';
    this.textarea.readOnly = sending;
    this.sendButton.disabled = !this.canSend();
    const verb = paste ? 'Paste' : 'Send';
    this.sendButton.textContent = sending ? `${paste ? 'Pasting' : 'Sending'}…` : verb;
    this.cancelButton.hidden = !sending;
    this.clearButton.hidden = !paste || sending || this.textarea.value === '';
    for (const m of ['chat', 'paste'] as const) {
      this.modeButtons[m].setAttribute('aria-pressed', String(this.mode === m));
      this.modeButtons[m].disabled = sending && this.mode !== m;
    }
    this.root.dataset.mode = this.mode;
    const active = this.activeShortcut();
    for (const sc of SHORTCUTS) {
      const b = this.shortcuts[sc.key];
      b.disabled = sending || this.shortcutPrefix(sc.key) === null;
      b.setAttribute('aria-pressed', String(active === sc.key));
    }
    const reply = this.shortcuts.reply;
    const name = this.replyTarget?.split('-')[0];
    reply.textContent = name ? `Reply ${name}` : 'Reply';
    reply.title = name ? `Whisper ${this.replyTarget}` : 'No whispers yet';
    this.root.dataset.state = this.state;
  }
}

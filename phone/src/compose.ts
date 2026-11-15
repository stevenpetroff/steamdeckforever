// Compose bar: textarea, live UTF-8 byte counter, Send/Cancel and send state.
//
// States: composing → sending → sent (box cleared) | not_sent(reason) (text
// kept). sending times out after 10 s into not_sent(no_connection).
import { utf8Length } from './hmac';
import { MAX_MESSAGE_BYTES, SEND_WINDOW_MS, type SendResult } from './types';

export type ComposeState = 'composing' | 'sending' | 'sent' | 'not_sent';

export interface ComposeDeps {
  /** Queue text for sending; returns the send id, or null if it can't be sent at all. */
  send(text: string): string | null;
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

function tooLongCopy(over: number): string {
  return `Too long — remove ${over} ${over === 1 ? 'byte' : 'bytes'}`;
}

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

  state: ComposeState = 'composing';
  reason: string | null = null;
  private pendingId: string | null = null;
  private timeout: ReturnType<typeof setTimeout> | null = null;
  private pendingReply: string | null = null;

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

    const row = el('div', 'compose-row');
    this.textarea = el('textarea', 'compose-input');
    this.textarea.rows = 2;
    this.textarea.placeholder = 'Say something… or /w, /g, /p';
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
    actions.append(this.sendButton, this.cancelButton);
    row.append(this.textarea, actions);

    const meta = el('div', 'compose-meta');
    this.warning = el('span', 'compose-warning');
    this.warning.hidden = true;
    this.counter = el('span', 'compose-counter');
    this.counter.setAttribute('aria-live', 'off');
    meta.append(this.warning, this.counter);

    this.root.append(this.confirmBox, this.notice, row, meta);
    host.append(this.root);

    this.textarea.addEventListener('keydown', (ev) => {
      // Enter never inserts a newline and never sends: only the button sends.
      if (ev.key === 'Enter') ev.preventDefault();
    });
    this.textarea.addEventListener('input', () => this.onInput());
    this.sendButton.addEventListener('click', () => this.submit());
    this.cancelButton.addEventListener('click', () => this.cancelSend());
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
    this.textarea.value = value.replace(NEWLINES, ' ');
    this.refresh();
  }

  /** Bytes currently in the box, as WoW counts them. */
  get bytes(): number {
    return utf8Length(this.textarea.value);
  }

  /**
   * Prefill a whisper reply to a player (R5). Replaces the box only when it's
   * empty or holds just a /w prefill; otherwise asks first.
   */
  replyTo(sender: string, realm?: string): void {
    if (this.state === 'sending') return;
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

  /** Apply a send_result from the socket. */
  handleResult(result: SendResult): void {
    if (this.state !== 'sending' || result.id !== this.pendingId) return;
    this.finishPending();
    if (result.outcome === 'sent') {
      this.state = 'sent';
      this.reason = null;
      this.textarea.value = '';
      this.showNotice('Sent', 'ok');
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
    const clean = this.textarea.value.replace(NEWLINES, ' ');
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
    return this.state !== 'sending' && t.trim() !== '' && utf8Length(t) <= MAX_MESSAGE_BYTES;
  }

  private submit(): void {
    if (!this.canSend()) return;
    this.hideConfirm();
    const text = this.textarea.value;
    const id = this.deps.send(text);
    if (id === null) {
      this.fail('no_connection');
      this.refresh();
      return;
    }
    this.pendingId = id;
    this.state = 'sending';
    this.reason = null;
    this.showNotice('Sending to WoW…', 'pending');
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
      const over = this.bytes - MAX_MESSAGE_BYTES;
      if (over > 0) copy = tooLongCopy(over);
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
    const over = bytes - MAX_MESSAGE_BYTES;
    this.counter.textContent = `${bytes}/${MAX_MESSAGE_BYTES}`;
    this.counter.classList.toggle('over', over > 0);
    this.warning.hidden = over <= 0;
    this.warning.textContent = over > 0 ? tooLongCopy(over) : '';
    const sending = this.state === 'sending';
    this.textarea.readOnly = sending;
    this.sendButton.disabled = !this.canSend();
    this.sendButton.textContent = sending ? 'Sending…' : 'Send';
    this.cancelButton.hidden = !sending;
    this.root.dataset.state = this.state;
  }
}

// WebSocket client for the deckchat service (docs/protocol.md).
//
// - The pairing secret arrives once in the URL fragment, is kept in
//   localStorage and stripped from the address bar.
// - Auth is challenge-response; the secret never crosses the network.
// - In-flight sends are resent with the same id after a reconnect, but only
//   inside the 10 s send window (the server dedupes by id, plan KTD7).
import { authMac, randomId, sendMac } from './hmac';
import { SEND_WINDOW_MS, type Message, type SendResult, type ServerFrame, type Status } from './types';

export const SECRET_KEY = 'deckchat.secret';
const SECRET_RE = /^[0-9a-f]{32}$/i;
/** WebSocket close code for a policy violation (bad or missing MAC). */
export const CLOSE_POLICY_VIOLATION = 1008;
const BACKOFF_MS = [1000, 2000, 4000, 8000, 10000];

export interface LocationLike {
  hash: string;
  pathname: string;
  search: string;
}
export interface HistoryLike {
  replaceState(data: unknown, unused: string, url?: string | URL | null): void;
}

/**
 * Read a pairing secret from the URL fragment (once), save it, and strip the
 * fragment. Returns the secret to use: the new one, or the stored one.
 */
export function captureSecret(loc: LocationLike, storage: Storage, hist: HistoryLike): string | null {
  const frag = loc.hash.replace(/^#/, '');
  if (SECRET_RE.test(frag)) {
    storage.setItem(SECRET_KEY, frag.toLowerCase());
    hist.replaceState(null, '', loc.pathname + loc.search);
  }
  const stored = storage.getItem(SECRET_KEY);
  return stored && SECRET_RE.test(stored) ? stored : null;
}

export type ConnState = 'connecting' | 'connected' | 'disconnected' | 'not_paired';

export interface SocketHandlers {
  onState?(state: ConnState): void;
  onHistory?(messages: Message[]): void;
  onMessage?(message: Message): void;
  onStatus?(status: Status): void;
  onSendResult?(result: SendResult): void;
}

export interface DeckSocketOptions {
  url: string;
  storage: Storage;
  handlers: SocketHandlers;
  WebSocketImpl?: typeof WebSocket;
  now?: () => number;
}

interface InFlight {
  id: string;
  /** Time of the first attempt; reused on resend so the server's 10 s check holds. */
  ts: number;
  text: string;
  /** Connection this send was last written to. */
  sentOn: WebSocket | null;
}

export class DeckSocket {
  private ws: WebSocket | null = null;
  private nonce: string | null = null;
  private authed = false;
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;
  private inflight = new Map<string, InFlight>();
  private _state: ConnState = 'disconnected';
  private readonly WS: typeof WebSocket;
  private readonly now: () => number;

  constructor(private readonly opts: DeckSocketOptions) {
    this.WS = opts.WebSocketImpl ?? WebSocket;
    this.now = opts.now ?? Date.now;
  }

  get state(): ConnState {
    return this._state;
  }

  start(): void {
    this.stopped = false;
    this.connect();
  }

  stop(): void {
    this.stopped = true;
    this.clearTimer();
    this.ws?.close();
    this.ws = null;
  }

  /**
   * Queue a message for sending. Returns its id, or null when not paired.
   * The frame goes out now if authenticated, otherwise after the next
   * handshake (if still inside the send window).
   */
  send(text: string): string | null {
    if (this._state === 'not_paired' || !this.secret()) return null;
    const entry: InFlight = { id: randomId(), ts: this.now(), text, sentOn: null };
    this.inflight.set(entry.id, entry);
    if (this.authed) this.write(entry);
    return entry.id;
  }

  /** Stop tracking a send: it won't be resent and its result is ignored. */
  cancel(id: string): void {
    this.inflight.delete(id);
  }

  private secret(): string | null {
    const s = this.opts.storage.getItem(SECRET_KEY);
    return s && SECRET_RE.test(s) ? s : null;
  }

  private setState(s: ConnState): void {
    if (s === this._state && s !== 'not_paired') return;
    this._state = s;
    this.opts.handlers.onState?.(s);
  }

  private notPaired(): void {
    this.opts.storage.removeItem(SECRET_KEY);
    this.clearTimer();
    this.inflight.clear();
    this.stopped = true;
    this.setState('not_paired');
  }

  private clearTimer(): void {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
  }

  private connect(): void {
    this.clearTimer();
    if (this.stopped) return;
    if (!this.secret()) {
      this.notPaired();
      return;
    }
    this.setState('connecting');
    this.authed = false;
    this.nonce = null;
    const ws = new this.WS(this.opts.url);
    this.ws = ws;
    ws.onmessage = (ev: MessageEvent) => {
      if (ws !== this.ws) return;
      this.onFrame(ev.data);
    };
    ws.onclose = (ev: CloseEvent) => {
      if (ws !== this.ws) return;
      this.ws = null;
      this.authed = false;
      if (ev.code === CLOSE_POLICY_VIOLATION) {
        this.notPaired();
        return;
      }
      if (this.stopped) return;
      this.setState('disconnected');
      const delay = BACKOFF_MS[Math.min(this.attempt, BACKOFF_MS.length - 1)];
      this.attempt++;
      this.timer = setTimeout(() => this.connect(), delay);
    };
  }

  private onFrame(data: unknown): void {
    if (typeof data !== 'string') return;
    let frame: ServerFrame;
    try {
      frame = JSON.parse(data) as ServerFrame;
    } catch {
      return;
    }
    const h = this.opts.handlers;
    switch (frame.type) {
      case 'challenge': {
        const secret = this.secret();
        if (!secret || typeof frame.nonce !== 'string') return;
        this.nonce = frame.nonce;
        this.ws?.send(JSON.stringify({ type: 'auth', mac: authMac(secret, frame.nonce) }));
        break;
      }
      case 'history':
        // The server sends history only after a successful auth.
        if (!this.authed && this.nonce) {
          this.authed = true;
          this.attempt = 0;
          this.setState('connected');
          this.flushInFlight();
        }
        h.onHistory?.(Array.isArray(frame.messages) ? frame.messages : []);
        break;
      case 'message':
        if (frame.message) h.onMessage?.(frame.message);
        break;
      case 'status':
        if (frame.status) h.onStatus?.(frame.status);
        break;
      case 'send_result': {
        // Results for unknown ids (already resolved, cancelled, or replayed
        // after a resend) are ignored so each send resolves once.
        if (!this.inflight.has(frame.id)) return;
        this.inflight.delete(frame.id);
        const result: SendResult = { id: frame.id, outcome: frame.outcome };
        if (frame.reason !== undefined) result.reason = frame.reason;
        h.onSendResult?.(result);
        break;
      }
    }
  }

  private flushInFlight(): void {
    const now = this.now();
    for (const entry of [...this.inflight.values()]) {
      if (now - entry.ts >= SEND_WINDOW_MS) {
        // Too old: the server would reject it as stale. The compose box's own
        // 10 s timeout reports no_connection.
        this.inflight.delete(entry.id);
        continue;
      }
      if (entry.sentOn !== this.ws) this.write(entry);
    }
  }

  private write(entry: InFlight): void {
    const secret = this.secret();
    if (!this.ws || !this.nonce || !secret) return;
    entry.sentOn = this.ws;
    this.ws.send(
      JSON.stringify({
        type: 'send',
        id: entry.id,
        ts: entry.ts,
        text: entry.text,
        mac: sendMac(secret, this.nonce, entry.id, entry.ts, entry.text),
      }),
    );
  }
}

/** ws:// URL for the page's own host (wss:// if ever served over https). */
export function socketUrl(loc: Pick<Location, 'protocol' | 'host'>): string {
  return `${loc.protocol === 'https:' ? 'wss' : 'ws'}://${loc.host}/ws`;
}

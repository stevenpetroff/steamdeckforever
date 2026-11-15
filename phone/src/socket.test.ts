import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { authMac, sendMac } from './hmac';
import { DeckSocket, SECRET_KEY, captureSecret, type ConnState } from './socket';
import type { SendResult } from './types';

const SECRET = '0123456789abcdef0123456789abcdef';

class MockWS {
  static instances: MockWS[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  readyState = 0;
  sent: any[] = [];
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  constructor(public url: string) {
    MockWS.instances.push(this);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.readyState = 3;
  }
  // --- test helpers (server side) ---
  open() {
    this.readyState = 1;
    this.onopen?.(new Event('open'));
  }
  frame(obj: unknown) {
    this.onmessage?.({ data: JSON.stringify(obj) } as MessageEvent);
  }
  drop(code = 1006) {
    this.readyState = 3;
    this.onclose?.({ code } as CloseEvent);
  }
  /** Run the handshake: challenge, (client auth), history, status. */
  handshake(nonce: string) {
    this.open();
    this.frame({ type: 'challenge', nonce });
    this.frame({ type: 'history', messages: [] });
    this.frame({
      type: 'status',
      status: { wow_focused: true, log_fresh: true, input_available: true, instance_paused: false },
    });
  }
  sends() {
    return this.sent.filter((f) => f.type === 'send');
  }
}

const last = () => MockWS.instances[MockWS.instances.length - 1];

function makeSocket(handlers: Partial<ConstructorParameters<typeof DeckSocket>[0]['handlers']> = {}) {
  const states: ConnState[] = [];
  const results: SendResult[] = [];
  const sock = new DeckSocket({
    url: 'ws://deck.local:7480/ws',
    storage: localStorage,
    WebSocketImpl: MockWS as unknown as typeof WebSocket,
    handlers: {
      onState: (s) => states.push(s),
      onSendResult: (r) => results.push(r),
      ...handlers,
    },
  });
  return { sock, states, results };
}

beforeEach(() => {
  MockWS.instances = [];
  localStorage.clear();
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-11-14T18:00:00Z'));
});
afterEach(() => {
  vi.useRealTimers();
});

describe('captureSecret', () => {
  it('saves a fragment secret and strips it from the URL', () => {
    const replaceState = vi.fn();
    const got = captureSecret(
      { hash: '#' + SECRET.toUpperCase(), pathname: '/', search: '' },
      localStorage,
      { replaceState },
    );
    expect(got).toBe(SECRET);
    expect(localStorage.getItem(SECRET_KEY)).toBe(SECRET);
    expect(replaceState).toHaveBeenCalledTimes(1);
    expect(replaceState.mock.calls[0][2]).toBe('/');
  });

  it('uses the stored secret on reload without a fragment', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const replaceState = vi.fn();
    const got = captureSecret({ hash: '', pathname: '/', search: '' }, localStorage, { replaceState });
    expect(got).toBe(SECRET);
    expect(replaceState).not.toHaveBeenCalled();
  });

  it('ignores a malformed fragment', () => {
    const got = captureSecret({ hash: '#nope', pathname: '/', search: '' }, localStorage, {
      replaceState: vi.fn(),
    });
    expect(got).toBeNull();
    expect(localStorage.getItem(SECRET_KEY)).toBeNull();
  });
});

describe('DeckSocket handshake and dispatch', () => {
  it('answers the challenge with the auth MAC and dispatches frames', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const onHistory = vi.fn();
    const onMessage = vi.fn();
    const onStatus = vi.fn();
    const { sock, states } = makeSocket({ onHistory, onMessage, onStatus });
    sock.start();
    const ws = last();
    expect(ws.url).toBe('ws://deck.local:7480/ws');
    ws.open();
    ws.frame({ type: 'challenge', nonce: 'aa'.repeat(16) });
    expect(ws.sent[0]).toEqual({ type: 'auth', mac: authMac(SECRET, 'aa'.repeat(16)) });
    ws.frame({ type: 'history', messages: [{ id: '1', time: 't', channel: 'say', text: 'x' }] });
    ws.frame({ type: 'message', message: { id: '2', time: 't', channel: 'say', text: 'y' } });
    ws.frame({
      type: 'status',
      status: { wow_focused: false, log_fresh: true, input_available: true, instance_paused: false },
    });
    expect(onHistory).toHaveBeenCalledWith([{ id: '1', time: 't', channel: 'say', text: 'x' }]);
    expect(onMessage).toHaveBeenCalledWith({ id: '2', time: 't', channel: 'say', text: 'y' });
    expect(onStatus).toHaveBeenCalledWith(expect.objectContaining({ wow_focused: false }));
    expect(states).toContain('connected');
  });

  it('sends with a 16-hex id, ts = now and the protocol MAC', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock } = makeSocket();
    sock.start();
    const nonce = 'bb'.repeat(16);
    last().handshake(nonce);
    const id = sock.send('hello Azeroth')!;
    expect(id).toMatch(/^[0-9a-f]{16}$/);
    const [frame] = last().sends();
    const ts = Date.now();
    expect(frame).toEqual({
      type: 'send',
      id,
      ts,
      text: 'hello Azeroth',
      mac: sendMac(SECRET, nonce, id, ts, 'hello Azeroth'),
    });
  });
});

describe('reconnect', () => {
  it('backs off 1s, 2s, 4s, 8s, then caps at 10s', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock } = makeSocket();
    sock.start();
    const delays = [1000, 2000, 4000, 8000, 10000, 10000];
    for (const d of delays) {
      const n = MockWS.instances.length;
      last().drop();
      vi.advanceTimersByTime(d - 1);
      expect(MockWS.instances.length).toBe(n);
      vi.advanceTimersByTime(1);
      expect(MockWS.instances.length).toBe(n + 1);
    }
  });

  it('resets backoff after a successful handshake', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock } = makeSocket();
    sock.start();
    last().drop();
    vi.advanceTimersByTime(1000);
    last().drop();
    vi.advanceTimersByTime(2000);
    last().handshake('cc'.repeat(16));
    const n = MockWS.instances.length;
    last().drop();
    vi.advanceTimersByTime(1000);
    expect(MockWS.instances.length).toBe(n + 1);
  });

  it('resends an in-flight send with the same id after reconnect inside 10s, ending in sent once', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock, results } = makeSocket();
    sock.start();
    last().handshake('01'.repeat(16));
    const id = sock.send('inc')!;
    const ts = Date.now();
    last().drop();
    vi.advanceTimersByTime(1000);
    const ws2 = last();
    const nonce2 = '02'.repeat(16);
    ws2.handshake(nonce2);
    const resent = ws2.sends();
    expect(resent).toHaveLength(1);
    expect(resent[0]).toEqual({
      type: 'send',
      id,
      ts,
      text: 'inc',
      mac: sendMac(SECRET, nonce2, id, ts, 'inc'),
    });
    ws2.frame({ type: 'send_result', id, outcome: 'sent' });
    // A duplicate replay of the stored result must not be reported twice.
    ws2.frame({ type: 'send_result', id, outcome: 'sent' });
    expect(results).toEqual([{ id, outcome: 'sent' }]);
    // And it is no longer in flight: another reconnect doesn't resend.
    ws2.drop();
    vi.advanceTimersByTime(1000);
    last().handshake('03'.repeat(16));
    expect(last().sends()).toHaveLength(0);
  });

  it('does not resend after the 10s window', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock } = makeSocket();
    sock.start();
    last().handshake('01'.repeat(16));
    sock.send('late');
    last().drop();
    // Stay offline past the window.
    vi.advanceTimersByTime(1000);
    last().drop();
    vi.advanceTimersByTime(2000);
    last().drop();
    vi.advanceTimersByTime(4000);
    last().drop();
    vi.advanceTimersByTime(8000);
    last().handshake('04'.repeat(16));
    expect(last().sends()).toHaveLength(0);
  });

  it('a cancelled send is not resent', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock } = makeSocket();
    sock.start();
    last().handshake('01'.repeat(16));
    const id = sock.send('nvm')!;
    last().drop();
    sock.cancel(id);
    vi.advanceTimersByTime(1000);
    last().handshake('05'.repeat(16));
    expect(last().sends()).toHaveLength(0);
  });
});

describe('not paired', () => {
  it('with no stored secret, shows not_paired and never connects', () => {
    const { sock, states } = makeSocket();
    sock.start();
    expect(states).toEqual(['not_paired']);
    vi.advanceTimersByTime(60_000);
    expect(MockWS.instances).toHaveLength(0);
    expect(sock.send('x')).toBeNull();
  });

  it('on close 1008, clears the secret and stops retrying', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock, states } = makeSocket();
    sock.start();
    const ws = last();
    ws.open();
    ws.frame({ type: 'challenge', nonce: 'dd'.repeat(16) });
    ws.drop(1008);
    expect(states[states.length - 1]).toBe('not_paired');
    expect(localStorage.getItem(SECRET_KEY)).toBeNull();
    vi.advanceTimersByTime(60_000);
    expect(MockWS.instances).toHaveLength(1);
  });

  it('on close 4000 (slow handshake), keeps the secret and reconnects', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock, states } = makeSocket();
    sock.start();
    const ws = last();
    ws.open();
    ws.frame({ type: 'challenge', nonce: 'dd'.repeat(16) });
    ws.drop(4000);
    expect(states[states.length - 1]).toBe('disconnected');
    expect(localStorage.getItem(SECRET_KEY)).toBe(SECRET);
    vi.advanceTimersByTime(1_000);
    expect(MockWS.instances).toHaveLength(2);
  });

  it('on close 1008 with a send in flight, reports it not sent right away', () => {
    localStorage.setItem(SECRET_KEY, SECRET);
    const { sock, results } = makeSocket();
    sock.start();
    last().handshake('ee'.repeat(16));
    const id = sock.send('/g hi');
    expect(id).not.toBeNull();
    last().drop(1008);
    expect(results).toEqual([{ id, outcome: 'not_sent', reason: 'no_connection' }]);
  });
});

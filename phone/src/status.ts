// Status line and not-paired screen.
//
// Blocking states (not paired, disconnected/connecting, keyboard unavailable)
// take precedence over informational ones (WoW not focused, stale log,
// chat paused in instance).
import type { ConnState } from './socket';
import type { Status } from './types';

export const NOT_PAIRED_COPY = 'Type /deckchat pair in WoW and scan the code';

export type StatusLevel = 'ok' | 'warn' | 'block';
export interface StatusLineValue {
  text: string;
  level: StatusLevel;
}

export function statusLine(conn: ConnState, status: Status | null): StatusLineValue {
  switch (conn) {
    case 'not_paired':
      return { text: 'Not paired', level: 'block' };
    case 'connecting':
      return { text: 'Connecting to your Deck…', level: 'block' };
    case 'disconnected':
      return { text: 'Disconnected — reconnecting…', level: 'block' };
    case 'connected':
      break;
  }
  if (!status) return { text: 'Connected', level: 'ok' };
  if (!status.input_available) {
    return { text: 'Keyboard unavailable — re-run the installer', level: 'block' };
  }
  const info: string[] = [];
  if (!status.wow_focused) info.push('WoW not focused');
  if (!status.log_fresh) info.push('Chat log is stale');
  if (status.instance_paused) info.push('Chat paused in instance');
  return info.length ? { text: info.join(' · '), level: 'warn' } : { text: 'Connected', level: 'ok' };
}

export class StatusBar {
  readonly line: HTMLElement;
  readonly notPaired: HTMLElement;
  private readonly lineText: HTMLElement;

  constructor(host: HTMLElement, overlayHost: HTMLElement = host) {
    this.line = document.createElement('div');
    this.line.className = 'status';
    this.line.setAttribute('role', 'status');
    this.line.setAttribute('aria-live', 'polite');
    const gem = document.createElement('span');
    gem.className = 'status-gem';
    gem.setAttribute('aria-hidden', 'true');
    this.lineText = document.createElement('span');
    this.lineText.className = 'status-text';
    this.line.append(gem, this.lineText);
    host.append(this.line);

    this.notPaired = document.createElement('section');
    this.notPaired.className = 'not-paired';
    this.notPaired.hidden = true;
    this.notPaired.setAttribute('aria-labelledby', 'not-paired-title');
    const panel = document.createElement('div');
    panel.className = 'not-paired-panel frame';
    const title = document.createElement('h2');
    title.id = 'not-paired-title';
    title.textContent = 'Not paired';
    const body = document.createElement('p');
    body.className = 'not-paired-copy';
    body.textContent = NOT_PAIRED_COPY;
    const hint = document.createElement('p');
    hint.className = 'not-paired-hint';
    hint.textContent = 'The code opens this page with a fresh key. Keep your phone on the same Wi-Fi as the Deck.';
    panel.append(title, body, hint);
    this.notPaired.append(panel);
    overlayHost.append(this.notPaired);
  }

  update(conn: ConnState, status: Status | null): void {
    const v = statusLine(conn, status);
    this.lineText.textContent = v.text;
    this.line.dataset.level = v.level;
    this.notPaired.hidden = conn !== 'not_paired';
  }
}

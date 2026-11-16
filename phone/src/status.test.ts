import { describe, expect, it } from 'vitest';
import { NOT_PAIRED_COPY, StatusBar, statusLine } from './status';
import type { Status } from './types';

const ok: Status = { wow_focused: true, log_fresh: true, input_available: true, instance_paused: false };

describe('statusLine precedence', () => {
  it('connected and healthy', () => {
    expect(statusLine('connected', ok)).toEqual({ text: 'Connected', level: 'ok' });
  });
  it('informational states', () => {
    expect(statusLine('connected', { ...ok, wow_focused: false }).text).toBe('WoW not focused');
    expect(statusLine('connected', { ...ok, log_fresh: false }).text).toBe('Chat log is stale');
    expect(statusLine('connected', { ...ok, instance_paused: true }).text).toBe('Chat paused in instance');
    expect(statusLine('connected', { ...ok, wow_focused: false, log_fresh: false })).toEqual({
      text: 'WoW not focused · Chat log is stale',
      level: 'warn',
    });
  });
  it('keyboard unavailable blocks and wins over informational states', () => {
    expect(statusLine('connected', { ...ok, input_available: false, wow_focused: false })).toEqual({
      text: 'Keyboard unavailable — re-run the installer',
      level: 'block',
    });
  });
  it('connection problems win over service status', () => {
    const bad = { ...ok, input_available: false };
    expect(statusLine('disconnected', bad).level).toBe('block');
    expect(statusLine('disconnected', bad).text).toMatch(/Disconnected/);
    expect(statusLine('connecting', null).text).toMatch(/Connecting/);
    expect(statusLine('not_paired', bad)).toEqual({ text: 'Not paired', level: 'block' });
  });
});

describe('StatusBar', () => {
  it('shows the not-paired screen only when not paired', () => {
    document.body.replaceChildren();
    const bar = new StatusBar(document.body);
    bar.update('connecting', null);
    expect(bar.notPaired.hidden).toBe(true);
    bar.update('not_paired', null);
    expect(bar.notPaired.hidden).toBe(false);
    expect(bar.notPaired.textContent).toContain(NOT_PAIRED_COPY);
    expect(NOT_PAIRED_COPY).toBe('Type /deckchat pair in WoW and scan the code');
    expect(bar.line.dataset.level).toBe('block');
  });
});

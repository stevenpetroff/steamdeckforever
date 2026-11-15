import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Compose, REASON_COPY } from './compose';

function setup(sendImpl?: (text: string) => string | null) {
  document.body.replaceChildren();
  const host = document.createElement('div');
  document.body.append(host);
  const send = vi.fn(sendImpl ?? (() => 'id1'));
  const cancel = vi.fn();
  const c = new Compose(host, { send, cancel });
  return { c, send, cancel, host };
}

function type(c: Compose, text: string) {
  c.textarea.value = text;
  c.textarea.dispatchEvent(new Event('input', { bubbles: true }));
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('byte limit (Covers AE3)', () => {
  it('256 bytes (128 two-byte chars) disables Send and warns', () => {
    const { c, send } = setup();
    type(c, 'é'.repeat(128));
    expect(c.counter.textContent).toBe('256/255');
    expect(c.sendButton.disabled).toBe(true);
    expect(c.warning.textContent).toBe('Too long — remove 1 byte');
    expect(c.warning.hidden).toBe(false);
    c.sendButton.click();
    expect(send).not.toHaveBeenCalled();
  });

  it('255 bytes enables Send', () => {
    const { c } = setup();
    type(c, 'é'.repeat(127) + 'a');
    expect(c.counter.textContent).toBe('255/255');
    expect(c.sendButton.disabled).toBe(false);
    expect(c.warning.hidden).toBe(true);
  });

  it('empty or whitespace-only text disables Send', () => {
    const { c } = setup();
    expect(c.sendButton.disabled).toBe(true);
    type(c, '   ');
    expect(c.sendButton.disabled).toBe(true);
  });
});

describe('newlines', () => {
  it('Enter inserts nothing and does not send', () => {
    const { c, send } = setup();
    type(c, 'hi');
    const ev = new KeyboardEvent('keydown', { key: 'Enter', cancelable: true, bubbles: true });
    c.textarea.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(send).not.toHaveBeenCalled();
  });

  it('pasted or dictated newlines become spaces', () => {
    const { c } = setup();
    type(c, 'one\ntwo\r\nthree\u2028four');
    expect(c.text).toBe('one two three four');
  });
});

describe('send states', () => {
  it('only the Send button sends; sending → sent clears the box', () => {
    const { c, send } = setup();
    type(c, 'brb');
    c.sendButton.click();
    expect(send).toHaveBeenCalledWith('brb');
    expect(c.state).toBe('sending');
    expect(c.cancelButton.hidden).toBe(false);
    c.handleResult({ id: 'id1', outcome: 'sent' });
    expect(c.state).toBe('sent');
    expect(c.text).toBe('');
  });

  it('Covers AE4. not_sent not_focused keeps the text and shows the reason', () => {
    const { c } = setup();
    type(c, '/p pulling');
    c.sendButton.click();
    c.handleResult({ id: 'id1', outcome: 'not_sent', reason: 'not_focused' });
    expect(c.state).toBe('not_sent');
    expect(c.text).toBe('/p pulling');
    expect(c.notice.textContent).toContain("WoW isn't in front — tap back into the game");
  });

  const reasons: Array<[string, string]> = [
    ['not_focused', "WoW isn't in front — tap back into the game"],
    ['focus_lost', 'WoW lost focus mid-send — try again'],
    ['invalid_text', "Can't send empty text or line breaks"],
    ['input_unavailable', 'Deck keyboard unavailable — re-run the installer'],
    ['stale', 'Took too long — try again'],
    ['no_connection', 'Not connected to your Deck'],
  ];
  for (const [reason, copy] of reasons) {
    it(`not_sent ${reason} shows "${copy}" and keeps the text`, () => {
      expect(REASON_COPY[reason]).toBe(copy);
      const { c } = setup();
      type(c, 'hello');
      c.sendButton.click();
      c.handleResult({ id: 'id1', outcome: 'not_sent', reason });
      expect(c.text).toBe('hello');
      expect(c.notice.textContent).toContain(copy);
    });
  }

  it('not_sent too_long shows how many bytes to remove', () => {
    const { c } = setup();
    type(c, 'hello');
    c.sendButton.click();
    c.handleResult({ id: 'id1', outcome: 'not_sent', reason: 'too_long' });
    expect(c.text).toBe('hello');
    expect(c.notice.textContent).toMatch(/too long/i);
  });

  it('times out after 10s into not_sent(no_connection), text restored, send cancelled', () => {
    const { c, cancel } = setup();
    type(c, 'anyone?');
    c.sendButton.click();
    vi.advanceTimersByTime(9_999);
    expect(c.state).toBe('sending');
    vi.advanceTimersByTime(1);
    expect(c.state).toBe('not_sent');
    expect(c.reason).toBe('no_connection');
    expect(c.text).toBe('anyone?');
    expect(c.notice.textContent).toContain('Not connected to your Deck');
    expect(cancel).toHaveBeenCalledWith('id1');
    // A late result is ignored.
    c.handleResult({ id: 'id1', outcome: 'sent' });
    expect(c.text).toBe('anyone?');
  });

  it('not paired (send returns null) is not_sent(no_connection) immediately', () => {
    const { c } = setup(() => null);
    type(c, 'x');
    c.sendButton.click();
    expect(c.state).toBe('not_sent');
    expect(c.reason).toBe('no_connection');
  });

  it('Cancel while sending returns to composing with the text kept', () => {
    const { c, cancel } = setup();
    type(c, 'oops');
    c.sendButton.click();
    c.cancelButton.click();
    expect(cancel).toHaveBeenCalledWith('id1');
    expect(c.state).toBe('composing');
    expect(c.text).toBe('oops');
    expect(c.cancelButton.hidden).toBe(true);
  });

  it('ignores results for other ids', () => {
    const { c } = setup();
    type(c, 'x');
    c.sendButton.click();
    c.handleResult({ id: 'other', outcome: 'sent' });
    expect(c.state).toBe('sending');
  });
});

describe('replyTo', () => {
  it('fills an empty box with /w Name-Realm and puts the caret at the end', () => {
    const { c } = setup();
    c.replyTo('Thrall', 'Doomhowl');
    expect(c.text).toBe('/w Thrall-Doomhowl ');
    expect(c.textarea.selectionStart).toBe('/w Thrall-Doomhowl '.length);
    expect(c.textarea.selectionEnd).toBe('/w Thrall-Doomhowl '.length);
  });

  it('omits the realm when absent and replaces a bare /w prefill', () => {
    const { c } = setup();
    c.replyTo('Thrall', 'Doomhowl');
    c.replyTo('Jaina');
    expect(c.text).toBe('/w Jaina ');
    expect(c.confirmBox.hidden).toBe(true);
  });

  it('with a draft, asks first; declining keeps the draft', () => {
    const { c } = setup();
    type(c, 'half a thought');
    c.replyTo('Thrall', 'Doomhowl');
    expect(c.confirmBox.hidden).toBe(false);
    expect(c.confirmBox.textContent).toContain('Replace draft with reply to Thrall?');
    expect(c.text).toBe('half a thought');
    c.confirmNo.click();
    expect(c.confirmBox.hidden).toBe(true);
    expect(c.text).toBe('half a thought');
  });

  it('with a draft, accepting replaces it', () => {
    const { c } = setup();
    type(c, '/w Thrall-Doomhowl see you');
    c.replyTo('Jaina');
    expect(c.confirmBox.hidden).toBe(false);
    c.confirmYes.click();
    expect(c.text).toBe('/w Jaina ');
  });
});

describe('touch targets', () => {
  it('buttons carry the 44px target class', () => {
    const { c } = setup();
    for (const b of [c.sendButton, c.cancelButton, c.confirmYes, c.confirmNo]) {
      expect(b.classList.contains('tap')).toBe(true);
    }
  });
});

import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  authMac,
  bytesToHex,
  hexToBytes,
  hmacSha256,
  randomId,
  sendMac,
  pasteMac,
  sha256,
  utf8,
} from './hmac';

const hex = (s: string) => hexToBytes(s);

describe('sha256', () => {
  it('matches FIPS 180-2 / node:crypto digests', () => {
    expect(bytesToHex(sha256(utf8('')))).toBe(
      'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    );
    expect(bytesToHex(sha256(utf8('abc')))).toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    );
    // Multi-block input with a non-ASCII tail (computed with node:crypto).
    expect(bytesToHex(sha256(utf8('a'.repeat(1000) + 'é')))).toBe(
      '86e4ed601abb881a03f08899c4faa0e94ace671a8eb89ad1c484bc64df5ada8f',
    );
  });
});

describe('hmacSha256 — RFC 4231 test vectors', () => {
  const cases: Array<[string, Uint8Array, Uint8Array, string]> = [
    [
      'case 1',
      hex('0b'.repeat(20)),
      utf8('Hi There'),
      'b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7',
    ],
    [
      'case 2',
      utf8('Jefe'),
      utf8('what do ya want for nothing?'),
      '5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843',
    ],
    [
      'case 3',
      hex('aa'.repeat(20)),
      hex('dd'.repeat(50)),
      '773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe',
    ],
    [
      'case 4',
      hex('0102030405060708090a0b0c0d0e0f10111213141516171819'),
      hex('cd'.repeat(50)),
      '82558a389a443c0ea4cc819899f2083a85f0faa3e578f8077a2e3ff46729665b',
    ],
    [
      'case 6 (key longer than block)',
      hex('aa'.repeat(131)),
      utf8('Test Using Larger Than Block-Size Key - Hash Key First'),
      '60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54',
    ],
    [
      'case 7',
      hex('aa'.repeat(131)),
      utf8(
        'This is a test using a larger than block-size key and a larger than block-size data. The key needs to be hashed before being used by the HMAC algorithm.',
      ),
      '9b09ffa71b942fcb27635fbcd5b0e944bfdc63644f0713938a7f51535c3a35e2',
    ],
  ];
  for (const [name, key, msg, want] of cases) {
    it(name, () => expect(bytesToHex(hmacSha256(key, msg))).toBe(want));
  }
  it('case 5 (truncated to 128 bits)', () => {
    const mac = hmacSha256(hex('0c'.repeat(20)), utf8('Test With Truncation'));
    expect(bytesToHex(mac).slice(0, 32)).toBe('a3b6167473100ee06e0c796c2955552b');
  });
});

describe('protocol MACs (docs/protocol.md)', () => {
  // Vectors computed at dev time with node:crypto createHmac('sha256', key).
  const secret = '000102030405060708090a0b0c0d0e0f';
  const nonce = '00112233445566778899aabbccddeeff';

  it('auth mac = HMAC(secret bytes, "auth|" + nonce)', () => {
    expect(authMac(secret, nonce)).toBe(
      '9c871b6d9c79d57b2cd315f390f5e608d6e3a16d2a971bb256627afc5983512c',
    );
  });

  it('send mac = HMAC(secret bytes, nonce|id|ts|text) with UTF-8 text', () => {
    expect(
      sendMac(secret, nonce, 'abc123def4567890', 1763143331204, 'héllo wörld ⚔️ /w Thrall-Doomhowl hi'),
    ).toBe('6f5119dcb978e1ba124e8489949cb404b07a6869c1b8c8bb83f937a221de3ad8');
  });

  it('paste mac = HMAC(secret bytes, "paste|" + nonce|id|ts|text), newlines kept', () => {
    // Vector computed with Python's hmac module.
    expect(pasteMac(secret, nonce, 'abc123def4567890', 1763143331204, '!WA:2!héllo\nline two')).toBe(
      '23c1c9a65ed4c80e43510ff214091da51d7cbf3ff2f5bbc00d47285c5b87a8d4',
    );
  });
});

// Cross-check against the Go server's vectors when that file is present.
// Vitest runs with cwd = phone/.
const goVectorsPath = resolve(
  process.cwd(),
  '../companion/internal/server/testdata/mac_vectors.json',
);

interface GoVector {
  name: string;
  secret: string;
  nonce: string;
  auth_mac: string;
  id: string;
  ts: number;
  text: string;
  send_mac: string;
}

describe.skipIf(!existsSync(goVectorsPath))('Go server mac_vectors.json', () => {
  const vectors = existsSync(goVectorsPath)
    ? (JSON.parse(readFileSync(goVectorsPath, 'utf8')) as GoVector[])
    : [];
  it('has vectors', () => expect(vectors.length).toBeGreaterThan(0));
  for (const v of vectors) {
    it(`${v.name}: auth and send MACs match the Go server`, () => {
      expect(authMac(v.secret, v.nonce)).toBe(v.auth_mac);
      expect(sendMac(v.secret, v.nonce, v.id, v.ts, v.text)).toBe(v.send_mac);
    });
  }
});

describe('randomId', () => {
  it('is 16 lowercase hex chars and varies', () => {
    const a = randomId();
    const b = randomId();
    expect(a).toMatch(/^[0-9a-f]{16}$/);
    expect(b).toMatch(/^[0-9a-f]{16}$/);
    expect(a).not.toBe(b);
  });
});

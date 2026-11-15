// Pure-TypeScript SHA-256, HMAC-SHA256 and random IDs.
//
// The phone page is served over plain HTTP on the LAN, which is not a secure
// context, so crypto.subtle and crypto.randomUUID are unavailable (plan KTD7).
// crypto.getRandomValues is available on every origin and is used for IDs.

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

const encoder = new TextEncoder();

export function utf8(s: string): Uint8Array {
  return encoder.encode(s);
}

export function utf8Length(s: string): number {
  return encoder.encode(s).length;
}

export function sha256(data: Uint8Array): Uint8Array {
  const bitLen = data.length * 8;
  // Message + 0x80 + zero padding + 64-bit length, rounded to 64-byte blocks.
  const padded = new Uint8Array(((data.length + 9 + 63) >> 6) << 6);
  padded.set(data);
  padded[data.length] = 0x80;
  const view = new DataView(padded.buffer);
  view.setUint32(padded.length - 8, Math.floor(bitLen / 0x100000000));
  view.setUint32(padded.length - 4, bitLen >>> 0);

  const h = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ]);
  const w = new Uint32Array(64);
  const rotr = (x: number, n: number) => (x >>> n) | (x << (32 - n));

  for (let off = 0; off < padded.length; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(off + i * 4);
    for (let i = 16; i < 64; i++) {
      const a = w[i - 15];
      const b = w[i - 2];
      const s0 = rotr(a, 7) ^ rotr(a, 18) ^ (a >>> 3);
      const s1 = rotr(b, 17) ^ rotr(b, 19) ^ (b >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
    }
    let a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
    for (let i = 0; i < 64; i++) {
      const S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const ch = (e & f) ^ (~e & g);
      const t1 = (hh + S1 + ch + K[i] + w[i]) >>> 0;
      const S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + maj) >>> 0;
      hh = g;
      g = f;
      f = e;
      e = (d + t1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (t1 + t2) >>> 0;
    }
    h[0] += a; h[1] += b; h[2] += c; h[3] += d;
    h[4] += e; h[5] += f; h[6] += g; h[7] += hh;
  }

  const out = new Uint8Array(32);
  const outView = new DataView(out.buffer);
  for (let i = 0; i < 8; i++) outView.setUint32(i * 4, h[i]);
  return out;
}

const BLOCK = 64;

export function hmacSha256(key: Uint8Array, message: Uint8Array): Uint8Array {
  const k = key.length > BLOCK ? sha256(key) : key;
  const block = new Uint8Array(BLOCK);
  block.set(k);
  const inner = new Uint8Array(BLOCK + message.length);
  const outer = new Uint8Array(BLOCK + 32);
  for (let i = 0; i < BLOCK; i++) {
    inner[i] = block[i] ^ 0x36;
    outer[i] = block[i] ^ 0x5c;
  }
  inner.set(message, BLOCK);
  outer.set(sha256(inner), BLOCK);
  return sha256(outer);
}

export function bytesToHex(bytes: Uint8Array): string {
  let s = '';
  for (const b of bytes) s += b.toString(16).padStart(2, '0');
  return s;
}

export function hexToBytes(hex: string): Uint8Array {
  if (hex.length % 2 !== 0 || /[^0-9a-fA-F]/.test(hex)) throw new Error('invalid hex');
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}

/** Random client message ID: 16 lowercase hex characters (64 bits). */
export function randomId(): string {
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  return bytesToHex(bytes);
}

/** auth frame MAC: HMAC(secret, "auth|" + nonce). See docs/protocol.md. */
export function authMac(secretHex: string, nonce: string): string {
  return bytesToHex(hmacSha256(hexToBytes(secretHex), utf8('auth|' + nonce)));
}

function sendInput(nonce: string, id: string, ts: number, text: string): string {
  return `${nonce}|${id}|${Math.trunc(ts).toString(10)}|${text}`;
}

/** send frame MAC: HMAC(secret, nonce|id|ts|text). See docs/protocol.md. */
export function sendMac(secretHex: string, nonce: string, id: string, ts: number, text: string): string {
  return bytesToHex(hmacSha256(hexToBytes(secretHex), utf8(sendInput(nonce, id, ts, text))));
}

/** paste frame MAC: HMAC(secret, "paste|" + nonce|id|ts|text). See docs/protocol.md. */
export function pasteMac(secretHex: string, nonce: string, id: string, ts: number, text: string): string {
  return bytesToHex(hmacSha256(hexToBytes(secretHex), utf8('paste|' + sendInput(nonce, id, ts, text))));
}

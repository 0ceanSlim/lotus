// throwaway-key.js — one-time Nostr keys for anonymous uploads.
//
// The 0x0 page uploads without a sign-in: it makes a fresh secp256k1 keypair
// in the browser, signs the Blossom kind-24242 authorization with it and
// shows the nsec once so the visitor can delete the file later if they want.
// The secret never leaves the page.
//
// This is a dependency-free BIP-340 Schnorr signer (BigInt arithmetic,
// WebCrypto SHA-256) plus bech32 for nsec/npub and NIP-01 event signing. It
// is used only for throwaway keys, so it is written for clarity, not for
// constant-time execution; real keys are signed by mill. The Go test
// internal/api/throwaway_key_test.go runs it under node against the BIP-340
// vectors and the server's own signature check.

(function (root) {
  'use strict';

  // ── secp256k1 ─────────────────────────────────────────────────────────
  const P = 2n ** 256n - 2n ** 32n - 977n;
  const N = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141n;
  const G = {
    x: 0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798n,
    y: 0x483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8n,
    z: 1n,
  };

  function mod(a, m) {
    if (m === undefined) m = P;
    const r = a % m;
    return r >= 0n ? r : r + m;
  }

  function modInv(a, m) {
    if (m === undefined) m = P;
    let t = 0n, newT = 1n, r = m, newR = mod(a, m);
    while (newR !== 0n) {
      const q = r / newR;
      [t, newT] = [newT, t - q * newT];
      [r, newR] = [newR, r - q * newR];
    }
    if (r !== 1n) throw new Error('not invertible');
    return mod(t, m);
  }

  // Jacobian coordinates; null is the point at infinity. a = 0 for secp256k1.
  function jDouble(p) {
    if (!p || p.y === 0n) return null;
    const { x: X1, y: Y1, z: Z1 } = p;
    const YY = mod(Y1 * Y1);
    const S = mod(4n * X1 * YY);
    const M = mod(3n * X1 * X1);
    const X3 = mod(M * M - 2n * S);
    const Y3 = mod(M * (S - X3) - 8n * mod(YY * YY));
    const Z3 = mod(2n * Y1 * Z1);
    return { x: X3, y: Y3, z: Z3 };
  }

  function jAdd(p, q) {
    if (!p) return q;
    if (!q) return p;
    const { x: X1, y: Y1, z: Z1 } = p;
    const { x: X2, y: Y2, z: Z2 } = q;
    const Z1Z1 = mod(Z1 * Z1), Z2Z2 = mod(Z2 * Z2);
    const U1 = mod(X1 * Z2Z2), U2 = mod(X2 * Z1Z1);
    const S1 = mod(Y1 * Z2 * Z2Z2), S2 = mod(Y2 * Z1 * Z1Z1);
    const H = mod(U2 - U1), R = mod(S2 - S1);
    if (H === 0n) return R === 0n ? jDouble(p) : null;
    const HH = mod(H * H), HHH = mod(H * HH);
    const V = mod(U1 * HH);
    const X3 = mod(R * R - HHH - 2n * V);
    const Y3 = mod(R * (V - X3) - S1 * HHH);
    const Z3 = mod(Z1 * Z2 * H);
    return { x: X3, y: Y3, z: Z3 };
  }

  function jMul(p, k) {
    let acc = null, base = p;
    while (k > 0n) {
      if (k & 1n) acc = jAdd(acc, base);
      base = jDouble(base);
      k >>= 1n;
    }
    return acc;
  }

  function toAffine(p) {
    if (!p) throw new Error('point at infinity');
    const zi = modInv(p.z);
    const zi2 = mod(zi * zi);
    return { x: mod(p.x * zi2), y: mod(p.y * zi2 * zi) };
  }

  // ── bytes, hex, hashing ──────────────────────────────────────────────
  function hexToBytes(hex) {
    if (typeof hex !== 'string' || hex.length % 2 || /[^0-9a-f]/i.test(hex)) throw new Error('bad hex');
    const out = new Uint8Array(hex.length / 2);
    for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.substr(i * 2, 2), 16);
    return out;
  }

  function bytesToHex(bytes) {
    let s = '';
    for (const b of bytes) s += b.toString(16).padStart(2, '0');
    return s;
  }

  function bytesToBig(bytes) {
    let v = 0n;
    for (const b of bytes) v = (v << 8n) | BigInt(b);
    return v;
  }

  function bigTo32(v) {
    const out = new Uint8Array(32);
    for (let i = 31; i >= 0; i--) { out[i] = Number(v & 0xffn); v >>= 8n; }
    return out;
  }

  function concat() {
    let n = 0;
    for (const a of arguments) n += a.length;
    const out = new Uint8Array(n);
    let o = 0;
    for (const a of arguments) { out.set(a, o); o += a.length; }
    return out;
  }

  const subtle = (root.crypto || {}).subtle;
  if (!subtle) throw new Error('throwaway-key.js needs WebCrypto');

  async function sha256(bytes) {
    return new Uint8Array(await subtle.digest('SHA-256', bytes));
  }

  const tagCache = new Map();
  async function taggedHash(tag) {
    let th = tagCache.get(tag);
    if (!th) {
      th = await sha256(new TextEncoder().encode(tag));
      tagCache.set(tag, th);
    }
    const parts = [th, th];
    for (let i = 1; i < arguments.length; i++) parts.push(arguments[i]);
    return sha256(concat.apply(null, parts));
  }

  // ── keys and signatures (BIP-340) ────────────────────────────────────
  function privToBig(privHex) {
    const d = bytesToBig(hexToBytes(privHex));
    if (hexToBytes(privHex).length !== 32 || d === 0n || d >= N) throw new Error('bad private key');
    return d;
  }

  function getPublicKey(privHex) {
    return bytesToHex(bigTo32(toAffine(jMul(G, privToBig(privHex))).x));
  }

  async function schnorrSign(msgHex, privHex, auxHex) {
    const msg = hexToBytes(msgHex);
    if (msg.length !== 32) throw new Error('message must be 32 bytes');
    let d = privToBig(privHex);
    const Pt = toAffine(jMul(G, d));
    if (Pt.y & 1n) d = N - d;
    const px = bigTo32(Pt.x);
    const aux = auxHex ? hexToBytes(auxHex) : root.crypto.getRandomValues(new Uint8Array(32));
    const auxHash = await taggedHash('BIP0340/aux', aux);
    const t = bigTo32(d);
    for (let i = 0; i < 32; i++) t[i] ^= auxHash[i];
    const rand = await taggedHash('BIP0340/nonce', t, px, msg);
    let k = mod(bytesToBig(rand), N);
    if (k === 0n) throw new Error('zero nonce');
    const R = toAffine(jMul(G, k));
    if (R.y & 1n) k = N - k;
    const rx = bigTo32(R.x);
    const e = mod(bytesToBig(await taggedHash('BIP0340/challenge', rx, px, msg)), N);
    return bytesToHex(concat(rx, bigTo32(mod(k + e * d, N))));
  }

  async function generate() {
    let priv;
    do {
      priv = root.crypto.getRandomValues(new Uint8Array(32));
    } while (bytesToBig(priv) === 0n || bytesToBig(priv) >= N);
    const privHex = bytesToHex(priv);
    const pubHex = getPublicKey(privHex);
    return { privHex, pubHex, nsec: nsecEncode(privHex), npub: npubEncode(pubHex) };
  }

  // ── NIP-01 events ────────────────────────────────────────────────────
  async function signEvent(template, privHex) {
    const pubkey = getPublicKey(privHex);
    const ev = {
      kind: template.kind,
      created_at: template.created_at,
      tags: template.tags || [],
      content: template.content || '',
      pubkey,
    };
    const serialized = JSON.stringify([0, ev.pubkey, ev.created_at, ev.kind, ev.tags, ev.content]);
    ev.id = bytesToHex(await sha256(new TextEncoder().encode(serialized)));
    ev.sig = await schnorrSign(ev.id, privHex);
    return ev;
  }

  // ── bech32 (BIP-173) ─────────────────────────────────────────────────
  const CHARSET = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l';
  const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];

  function polymod(values) {
    let chk = 1;
    for (const v of values) {
      const top = chk >>> 25;
      chk = ((chk & 0x1ffffff) << 5) ^ v;
      for (let i = 0; i < 5; i++) if ((top >>> i) & 1) chk ^= GEN[i];
    }
    return chk;
  }

  function hrpExpand(hrp) {
    const out = [];
    for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) >>> 5);
    out.push(0);
    for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) & 31);
    return out;
  }

  function toWords(bytes) {
    const words = [];
    let acc = 0, bits = 0;
    for (const b of bytes) {
      acc = (acc << 8) | b;
      bits += 8;
      while (bits >= 5) { bits -= 5; words.push((acc >>> bits) & 31); }
    }
    if (bits > 0) words.push((acc << (5 - bits)) & 31);
    return words;
  }

  function bech32Encode(hrp, bytes) {
    const words = toWords(bytes);
    const values = hrpExpand(hrp).concat(words, [0, 0, 0, 0, 0, 0]);
    const chk = polymod(values) ^ 1;
    let out = hrp + '1';
    for (const w of words) out += CHARSET[w];
    for (let i = 0; i < 6; i++) out += CHARSET[(chk >>> (5 * (5 - i))) & 31];
    return out;
  }

  function nsecEncode(privHex) { return bech32Encode('nsec', hexToBytes(privHex)); }
  function npubEncode(pubHex) { return bech32Encode('npub', hexToBytes(pubHex)); }

  root.ThrowawayKey = { generate, getPublicKey, schnorrSign, signEvent, nsecEncode, npubEncode, bytesToHex, hexToBytes };
})(typeof globalThis !== 'undefined' ? globalThis : window);

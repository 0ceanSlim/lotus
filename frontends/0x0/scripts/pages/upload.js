// upload.js — the 0x0 drop zone when the server has anonymous uploads on.
//
// Two ways to sign the Blossom authorization (kind 24242):
//   - a throwaway key made in the browser for this one upload (the default):
//     its nsec is shown once so the file can be deleted before it expires,
//     and forgotten with the page otherwise;
//   - a real key through mill (extension, bunker, Amber, pomegranate, pasted
//     key) after "sign in", so the file is listed under that pubkey and can
//     be deleted from any Blossom client later.
// Neither path creates a server session. Uploads carry their own signed
// authorization and the page keeps nothing but mill's own restore state plus
// which method/pubkey to restore. Blossom authorization tokens are base64url
// without padding (BUD-11); the short-link request to lotus's own API is a
// NIP-98 authorization signed by the same key.

(function () {
  'use strict';

  const limits = window.ZeroXZeroLimits || {};
  const MAX_BYTES = Number(limits.maxUploadBytes) || 0;
  const SIGNER_KEY = '0x0.signer';

  const $ = (id) => document.getElementById(id);
  const dropZone = $('drop-zone');
  const fileInput = $('file-input');
  const status = $('status');
  const result = $('result');
  const resultUrl = $('result-url');
  const progressWrap = $('progress-bar-wrap');
  const progressBar = $('progress-bar');
  const keyBox = $('key-box');
  const keyNsec = $('key-nsec');
  const copyBtn = $('copy-btn');
  const deleteBtn = $('delete-btn');
  const copyKeyBtn = $('copy-key-btn');
  const saveKeyBtn = $('save-key-btn');
  const shortBtn = $('short-btn');
  const foreignNote = $('foreign-note');
  const shortBox = $('short-box');
  const shortUrl = $('short-url');
  const copyShortBtn = $('copy-short-btn');
  const signinLinks = document.querySelectorAll('.signin-link');
  const topAccount = $('top-account');
  const topNpub = $('top-npub');
  const signoutLink = $('signout-link');
  const filesBox = $('my-files');
  const filesList = $('my-files-list');

  let signer = null;   // mill signer while signed in
  let current = null;  // { sha256, url, key } behind the result card
  let busy = false;

  // ── small helpers ────────────────────────────────────────────────────

  function setStatus(msg, type) {
    status.textContent = msg;
    status.className = type || 'dim';
  }

  function b64url(str) {
    const bytes = new TextEncoder().encode(str);
    let bin = '';
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  async function sha256hex(buffer) {
    const digest = await crypto.subtle.digest('SHA-256', buffer);
    return Array.from(new Uint8Array(digest)).map((b) => b.toString(16).padStart(2, '0')).join('');
  }

  function fmtBytes(n) {
    if (n >= 1073741824) return (n / 1073741824).toFixed(1).replace(/\.0$/, '') + ' GB';
    if (n >= 1048576) return (n / 1048576).toFixed(1).replace(/\.0$/, '') + ' MB';
    if (n >= 1024) return (n / 1024).toFixed(1).replace(/\.0$/, '') + ' KB';
    return n + ' bytes';
  }

  function flash(btn, text) {
    const old = btn.textContent;
    btn.textContent = text;
    setTimeout(() => { btn.textContent = old; }, 1500);
  }

  function copyText(text, btn) {
    navigator.clipboard.writeText(text).then(() => flash(btn, 'copied!'), () => flash(btn, 'copy failed'));
  }

  function shortKey(s) {
    return s.length > 20 ? s.slice(0, 12) + '…' + s.slice(-4) : s;
  }

  // ── signing ──────────────────────────────────────────────────────────

  function authTemplate(action, hash) {
    const now = Math.floor(Date.now() / 1000);
    return {
      kind: 24242,
      created_at: now,
      tags: [['t', action], ['x', hash], ['expiration', String(now + 300)]],
      content: '',
    };
  }

  // signWith signs with the throwaway key when one is given, otherwise with
  // the signed-in mill signer.
  function signWith(template, key) {
    return key
      ? window.ThrowawayKey.signEvent(template, key.privHex)
      : signer.signEvent(template);
  }

  // authHeader is the Blossom authorization (kind 24242, base64url).
  async function authHeader(action, hash, key) {
    const signed = await signWith(authTemplate(action, hash), key);
    return 'Nostr ' + b64url(JSON.stringify(signed));
  }

  // nip98Header is the authorization for lotus's own endpoints (kind 27235,
  // standard base64: that is what internal/nip98 decodes).
  async function nip98Header(method, url, key) {
    const template = {
      kind: 27235,
      created_at: Math.floor(Date.now() / 1000),
      tags: [['u', url], ['method', method]],
      content: '',
    };
    const signed = await signWith(template, key);
    const bytes = new TextEncoder().encode(JSON.stringify(signed));
    let bin = '';
    for (const b of bytes) bin += String.fromCharCode(b);
    return 'Nostr ' + btoa(bin);
  }

  // ── upload ───────────────────────────────────────────────────────────

  function request(method, url, header, body, type, onProgress) {
    return new Promise((resolve) => {
      const xhr = new XMLHttpRequest();
      xhr.open(method, url);
      xhr.setRequestHeader('Authorization', header);
      if (body) xhr.setRequestHeader('Content-Type', type || 'application/octet-stream');
      if (onProgress) xhr.upload.onprogress = onProgress;
      xhr.onload = () => resolve({
        status: xhr.status,
        text: xhr.responseText,
        reason: xhr.getResponseHeader('X-Reason'),
        retryAfter: xhr.getResponseHeader('Retry-After'),
      });
      xhr.onerror = () => resolve({ status: 0, text: '', reason: null, retryAfter: null });
      xhr.send(body ? new Blob([body], { type: type || 'application/octet-stream' }) : null);
    });
  }

  function failureMessage(res, what) {
    let msg = res.text;
    try { msg = JSON.parse(res.text).message || msg; } catch (_) { /* plain text */ }
    if (res.status === 0) return 'network error';
    if (res.status === 429) {
      const wait = res.retryAfter ? ' try again in ' + res.retryAfter + 's.' : '';
      return 'slow down: ' + (res.reason || msg || 'rate limited') + wait;
    }
    return what + ' failed: ' + (res.reason || msg || ('HTTP ' + res.status));
  }

  async function handleFile(file) {
    if (busy) return;
    busy = true;
    hideResult();
    progressWrap.style.display = 'none';
    progressBar.style.width = '0%';

    try {
      if (MAX_BYTES && file.size > MAX_BYTES) {
        setStatus('file too large. max ' + fmtBytes(MAX_BYTES) + '.', 'err');
        return;
      }

      setStatus('hashing file...', 'info');
      let buffer;
      try {
        buffer = await file.arrayBuffer();
      } catch (e) {
        setStatus('could not read file: ' + e.message, 'err');
        return;
      }
      const hash = await sha256hex(buffer);

      let key = null;
      if (!signer) {
        setStatus('making a one-time key...', 'info');
        key = await window.ThrowawayKey.generate();
      }

      setStatus(signer ? 'signing with your key...' : 'signing...', 'info');
      let header;
      try {
        header = await authHeader('upload', hash, key);
      } catch (e) {
        setStatus('signing cancelled or failed: ' + (e.message || e), 'err');
        return;
      }

      setStatus('uploading...', 'info');
      progressWrap.style.display = 'block';
      const res = await request('PUT', '/upload', header, buffer, file.type, (e) => {
        if (e.lengthComputable) progressBar.style.width = Math.round((e.loaded / e.total) * 100) + '%';
      });
      progressBar.style.width = '100%';

      if (res.status !== 200 && res.status !== 201) {
        setStatus(failureMessage(res, 'upload'), 'err');
        return;
      }
      let descriptor;
      try {
        descriptor = JSON.parse(res.text);
      } catch (_) {
        setStatus('upload succeeded but the response was unreadable', 'err');
        return;
      }
      // Identical bytes dedupe to the first uploader's blob: the descriptor
      // then names another pubkey, and neither this key nor a one-time key
      // can delete it. The card says so instead of offering buttons that
      // would fail.
      const ours = key ? key.pubHex : signer.pubkey;
      const foreign = !!(descriptor.pubkey && ours && descriptor.pubkey.toLowerCase() !== String(ours).toLowerCase());
      setStatus(foreign ? 'done. this file was already here.' : 'done.', 'ok');
      // The one-time key is kept for signing (a short link still needs a
      // signature) but not shown when it cannot delete the file.
      showResult({ sha256: descriptor.sha256 || hash, url: descriptor.url, key, foreign });
      if (signer) loadMyFiles();
    } finally {
      busy = false;
    }
  }

  // ── result card ──────────────────────────────────────────────────────

  function showResult(entry) {
    current = entry;
    resultUrl.textContent = entry.url;
    shortUrl.textContent = '';
    shortBox.style.display = 'none';
    foreignNote.style.display = entry.foreign ? 'block' : 'none';
    deleteBtn.style.display = entry.foreign ? 'none' : '';
    if (entry.key && !entry.foreign) {
      keyNsec.textContent = entry.key.nsec;
      keyBox.style.display = 'block';
    } else {
      keyNsec.textContent = '';
      keyBox.style.display = 'none';
    }
    result.style.display = 'block';
  }

  function hideResult() {
    current = null;
    result.style.display = 'none';
    keyNsec.textContent = '';
  }

  async function deleteCurrent() {
    if (!current || current.foreign || busy) return;
    busy = true;
    try {
      setStatus('signing delete...', 'info');
      let header;
      try {
        header = await authHeader('delete', current.sha256, current.key);
      } catch (e) {
        setStatus('signing cancelled or failed: ' + (e.message || e), 'err');
        return;
      }
      const res = await request('DELETE', '/' + current.sha256, header);
      if (res.status !== 200) {
        setStatus(failureMessage(res, 'delete'), 'err');
        return;
      }
      setStatus('deleted. the link is dead and the key is useless now.', 'ok');
      hideResult();
      if (signer) loadMyFiles();
    } finally {
      busy = false;
    }
  }

  // shortenCurrent asks the server for /s/<code> over the same key that owns
  // the blob. It is a button rather than automatic so a mill user is not
  // asked for a second signature on every upload.
  async function shortenCurrent() {
    if (!current || busy) return;
    busy = true;
    try {
      setStatus('signing short link request...', 'info');
      const url = window.location.origin + '/api/v1/short/' + current.sha256;
      let header;
      try {
        header = await nip98Header('POST', url, current.key);
      } catch (e) {
        setStatus('signing cancelled or failed: ' + (e.message || e), 'err');
        return;
      }
      let res, data = {};
      try {
        res = await fetch(url, { method: 'POST', headers: { Authorization: header } });
        data = await res.json().catch(() => ({}));
      } catch (_) {
        setStatus('network error', 'err');
        return;
      }
      if (!res.ok || !data.short_url) {
        setStatus('short link failed: ' + (data.message || ('HTTP ' + res.status)), 'err');
        return;
      }
      current.short = data.short_url;
      shortUrl.textContent = data.short_url;
      shortBox.style.display = 'block';
      setStatus('done.', 'ok');
    } finally {
      busy = false;
    }
  }

  // saveKey hands the visitor a small text file: the clipboard is lost on
  // the next copy, and without an extension a file is the only durable place
  // for a key that is worth nothing beyond deleting this one upload.
  function saveKey() {
    if (!current || !current.key) return;
    const text = [
      '0x0 one-time key',
      '',
      'file:  ' + current.url,
      current.short ? 'short: ' + current.short : null,
      'nsec:  ' + current.key.nsec,
      'npub:  ' + current.key.npub,
      '',
      'This key was made in your browser to sign one upload. It can delete that',
      'file before it expires (sign in with it on the 0x0 page, or use any',
      'Blossom client) and is good for nothing else.',
      '',
    ].filter((line) => line !== null).join('\n');
    const blob = new Blob([text], { type: 'text/plain' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = '0x0-key-' + current.sha256.slice(0, 8) + '.txt';
    document.body.appendChild(a);
    a.click();
    setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
    flash(saveKeyBtn, 'saved');
  }

  // ── mill sign-in ─────────────────────────────────────────────────────

  // mill applies a theme as inline --mill-* properties; these literal values
  // make the modal match this page: black, white, Courier, square. The
  // accent is a mid grey rather than white because mill's primary button
  // puts fixed white text on the accent colour.
  const MILL_THEME = {
    '--mill-bg': '#000',
    '--mill-surface': '#000',
    '--mill-card': '#000',
    '--mill-card-hover': '#111',
    '--mill-inset': '#0a0a0a',
    '--mill-inset-strong': '#151515',
    '--mill-overlay': 'rgba(0,0,0,0.85)',
    '--mill-border': '#444',
    '--mill-border-light': '#fff',
    '--mill-accent': '#777',
    '--mill-accent-hover': '#999',
    '--mill-accent-dim': 'rgba(255,255,255,0.12)',
    '--mill-teal': '#bbb',
    '--mill-teal-dim': 'rgba(255,255,255,0.08)',
    '--mill-text': '#fff',
    '--mill-text-secondary': '#bbb',
    '--mill-muted': '#777',
    '--mill-radius': '0',
    '--mill-shadow': 'none',
    '--mill-glow': 'transparent',
    '--mill-overlay-blur': '0',
    '--mill-font': "'Courier New', Courier, monospace",
    '--mill-font-mono': "'Courier New', Courier, monospace",
  };

  function millOptions() {
    return {
      theme: MILL_THEME,
      appName: '0x0',
      onConnected: onConnected,
      header: {
        logo: false,
        eyebrow: false,
        title: '0x0',
        message: 'sign in to upload under your own key.',
        align: 'left',
      },
      tip: false,
      // Extension and remote signer first, Google (pomegranate, the default
      // njump ecosystem) beside them; a pasted key, a brand-new key and Amber
      // under "more options". Read-only cannot sign and is left out. Amber
      // returns through the clipboard, so no callback route is needed.
      pomegranate: true,
      methods: ['nip07', 'nip46', 'pomegranate'],
      moreMethods: ['privatekey', 'newkey', 'nip55'],
      platforms: {
        android: { methods: ['nip55', 'nip46', 'pomegranate'], moreMethods: ['privatekey', 'newkey', 'nip07'] },
        ios: { methods: ['nip46', 'pomegranate', 'privatekey'], moreMethods: ['newkey'] },
      },
    };
  }

  function openSignIn(e) {
    if (e) e.preventDefault();
    if (!window.MILL) {
      setStatus('sign-in unavailable: the signer library did not load.', 'err');
      return;
    }
    window.MILL.open(millOptions());
  }

  function onConnected(res) {
    if (!res || !res.signer || typeof res.signer.signEvent !== 'function' || res.method === 'readonly') {
      setStatus('that sign-in method cannot sign uploads.', 'err');
      return;
    }
    signer = res.signer;
    try {
      localStorage.setItem(SIGNER_KEY, JSON.stringify({ method: res.method, pubkey: res.pubkey }));
    } catch (_) { /* private mode */ }
    if (window.MILL && typeof window.MILL.close === 'function') window.MILL.close();
    hideResult();
    setStatus('signed in. uploads now use your key.', 'ok');
    renderAccount();
    loadMyFiles();
  }

  function signOut(e) {
    if (e) e.preventDefault();
    try { if (signer && typeof signer.disconnect === 'function') signer.disconnect(); } catch (_) { /* best effort */ }
    try { if (window.MILL && typeof window.MILL.clearRestoreState === 'function') window.MILL.clearRestoreState(); } catch (_) { /* best effort */ }
    try { localStorage.removeItem(SIGNER_KEY); } catch (_) { /* private mode */ }
    signer = null;
    hideResult();
    renderAccount();
    setStatus('signed out. uploads use a one-time key again.', 'dim');
  }

  async function restoreSigner() {
    let saved = null;
    try { saved = JSON.parse(localStorage.getItem(SIGNER_KEY) || 'null'); } catch (_) { /* ignore */ }
    if (!saved || !saved.method || !saved.pubkey) return;
    if (!window.MILL || typeof window.MILL.restore !== 'function') return;
    // NIP-07 extensions inject window.nostr a little after the page loads.
    for (const delay of [0, 150, 400, 900, 1600]) {
      if (delay) await new Promise((r) => setTimeout(r, delay));
      try {
        const s = await window.MILL.restore({ method: saved.method, pubkey: saved.pubkey });
        if (s && typeof s.signEvent === 'function') {
          signer = s;
          renderAccount();
          loadMyFiles();
          return;
        }
      } catch (_) { /* try again */ }
      if (saved.method !== 'nip07' && saved.method !== 'browser_extension') break;
    }
  }

  function renderAccount() {
    if (signer) {
      const npub = signer.npub || (window.ThrowawayKey ? window.ThrowawayKey.npubEncode(signer.pubkey) : signer.pubkey);
      topNpub.textContent = shortKey(npub);
      topNpub.title = npub;
      topAccount.style.display = '';
      signinLinks.forEach((a) => { a.style.display = 'none'; });
    } else {
      topAccount.style.display = 'none';
      signinLinks.forEach((a) => { a.style.display = ''; });
      filesBox.style.display = 'none';
      filesList.textContent = '';
    }
  }

  // ── your files (signed in) ───────────────────────────────────────────

  async function loadMyFiles() {
    if (!signer) return;
    let blobs = [];
    try {
      const r = await fetch('/list/' + signer.pubkey, { cache: 'no-store' });
      if (r.ok) blobs = await r.json();
    } catch (_) { /* leave the list empty */ }
    if (!Array.isArray(blobs)) blobs = [];
    blobs.sort((a, b) => (b.uploaded || 0) - (a.uploaded || 0));
    filesList.textContent = '';
    if (!blobs.length) {
      filesBox.style.display = 'none';
      return;
    }
    for (const b of blobs) {
      const row = document.createElement('div');
      row.className = 'file-row';
      const link = document.createElement('a');
      link.href = b.url;
      link.target = '_blank';
      link.rel = 'noopener';
      link.textContent = b.url.replace(/^https?:\/\/[^/]+\//, '');
      const meta = document.createElement('span');
      meta.className = 'dim';
      meta.textContent = ' ' + fmtBytes(b.size || 0);
      const del = document.createElement('button');
      del.className = 'btn';
      del.textContent = 'delete';
      del.addEventListener('click', () => deleteListed(b.sha256, del));
      row.append(link, meta, del);
      filesList.appendChild(row);
    }
    filesBox.style.display = 'block';
  }

  async function deleteListed(sha256, btn) {
    if (!signer || busy) return;
    busy = true;
    btn.disabled = true;
    setStatus('signing delete...', 'info');
    try {
      const header = await authHeader('delete', sha256, null);
      const res = await request('DELETE', '/' + sha256, header);
      if (res.status !== 200) {
        setStatus(failureMessage(res, 'delete'), 'err');
        btn.disabled = false;
        return;
      }
      setStatus('deleted.', 'ok');
      if (current && current.sha256 === sha256) hideResult();
    } catch (e) {
      setStatus('signing cancelled or failed: ' + (e.message || e), 'err');
      btn.disabled = false;
      return;
    } finally {
      busy = false;
    }
    loadMyFiles();
  }

  // ── wiring ───────────────────────────────────────────────────────────

  dropZone.addEventListener('click', () => fileInput.click());
  dropZone.addEventListener('dragover', (e) => { e.preventDefault(); dropZone.classList.add('drag-over'); });
  dropZone.addEventListener('dragleave', () => dropZone.classList.remove('drag-over'));
  dropZone.addEventListener('drop', (e) => {
    e.preventDefault();
    dropZone.classList.remove('drag-over');
    if (e.dataTransfer.files.length) handleFile(e.dataTransfer.files[0]);
  });
  fileInput.addEventListener('change', () => {
    if (fileInput.files.length) handleFile(fileInput.files[0]);
    fileInput.value = '';
  });

  copyBtn.addEventListener('click', () => copyText(resultUrl.textContent, copyBtn));
  shortBtn.addEventListener('click', shortenCurrent);
  copyShortBtn.addEventListener('click', () => copyText(shortUrl.textContent, copyShortBtn));
  deleteBtn.addEventListener('click', deleteCurrent);
  copyKeyBtn.addEventListener('click', () => copyText(keyNsec.textContent, copyKeyBtn));
  saveKeyBtn.addEventListener('click', saveKey);
  signinLinks.forEach((a) => a.addEventListener('click', openSignIn));
  signoutLink.addEventListener('click', signOut);

  if (!window.ThrowawayKey) {
    setStatus('this browser cannot make keys (no WebCrypto); sign in to upload.', 'err');
  }
  renderAccount();
  restoreSigner();
})();

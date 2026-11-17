// media-process.js — optional processing before an upload, done entirely in
// the browser so the original file (and whatever it says about where and
// when it was made) never reaches the server.
//
//   strip     remove metadata: EXIF (camera, GPS, time), XMP, IPTC, comments
//             and text chunks. JPEG and PNG are cleaned losslessly by
//             dropping those segments; anything else is re-encoded.
//   compress  images: longest edge at most 2560px, re-encoded at 82%
//             quality (PNG becomes WebP). video: H.264 at most 1280px
//             wide, CRF 28, AAC 96k, in MP4. The result is used only when
//             it is smaller.
//
// Video and audio go through ffmpeg.wasm, loaded on first use from this
// site's own /scripts/vendor/ffmpeg (about 32 MB, cached after that).

(function () {
  'use strict';

  const IMAGE_ENCODE = { 'image/jpeg': 'image/jpeg', 'image/png': 'image/png', 'image/webp': 'image/webp' };
  const IMAGE_DECODE = ['image/jpeg', 'image/png', 'image/webp', 'image/avif', 'image/bmp'];
  const MAX_EDGE = 2560;
  const QUALITY = 0.82;

  function kind(file) {
    const t = (file.type || '').toLowerCase();
    if (t === 'image/gif' || t === 'image/svg+xml') return 'other'; // animation / markup: leave alone
    if (IMAGE_DECODE.includes(t)) return 'image';
    if (t.startsWith('video/')) return 'video';
    if (t.startsWith('audio/')) return 'audio';
    return 'other';
  }

  function canStrip(file) { return kind(file) !== 'other'; }
  function canCompress(file) { const k = kind(file); return k === 'image' || k === 'video'; }

  // ── lossless JPEG cleaning ─────────────────────────────────────────────

  // jpegOrientation reads the EXIF orientation (1 when absent). Dropping the
  // EXIF block from a rotated photo would show it sideways, so those are
  // re-encoded upright through a canvas instead.
  function jpegOrientation(bytes) {
    const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    let i = 2;
    while (i + 4 < bytes.length && bytes[i] === 0xff) {
      const marker = bytes[i + 1];
      const len = v.getUint16(i + 2);
      if (marker === 0xe1 && bytes[i + 4] === 0x45 && bytes[i + 5] === 0x78) { // "Exif"
        const tiff = i + 10;
        const le = v.getUint16(tiff) === 0x4949;
        const ifd = tiff + v.getUint32(tiff + 4, le);
        const n = v.getUint16(ifd, le);
        for (let k = 0; k < n; k++) {
          const e = ifd + 2 + k * 12;
          if (v.getUint16(e, le) === 0x0112) return v.getUint16(e + 8, le);
        }
        return 1;
      }
      if (marker === 0xda) break; // start of scan
      i += 2 + len;
    }
    return 1;
  }

  // stripJpeg keeps every segment except APP1 (EXIF, XMP), APP3–APP13
  // (vendor blocks, IPTC/Photoshop) and COM. APP0 (JFIF), APP2 (ICC colour
  // profile) and APP14 (Adobe colour transform) stay: they change how the
  // picture looks, not who made it.
  function stripJpeg(bytes) {
    if (bytes[0] !== 0xff || bytes[1] !== 0xd8) return null;
    const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const out = [bytes.subarray(0, 2)];
    let i = 2;
    while (i + 4 <= bytes.length) {
      if (bytes[i] !== 0xff) return null;
      const marker = bytes[i + 1];
      if (marker === 0xda) { out.push(bytes.subarray(i)); break; } // image data to the end
      const len = v.getUint16(i + 2);
      const drop = marker === 0xe1 || (marker >= 0xe3 && marker <= 0xed) || marker === 0xfe;
      if (!drop) out.push(bytes.subarray(i, i + 2 + len));
      i += 2 + len;
    }
    return new Blob(out, { type: 'image/jpeg' });
  }

  // stripPng drops text, time and EXIF chunks; pixels and colour chunks stay.
  function stripPng(bytes) {
    const sig = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
    if (!sig.every((b, k) => bytes[k] === b)) return null;
    const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const out = [bytes.subarray(0, 8)];
    const dropTypes = ['tEXt', 'zTXt', 'iTXt', 'eXIf', 'tIME'];
    let i = 8;
    while (i + 12 <= bytes.length) {
      const len = v.getUint32(i);
      const type = String.fromCharCode(bytes[i + 4], bytes[i + 5], bytes[i + 6], bytes[i + 7]);
      const end = i + 12 + len;
      if (!dropTypes.includes(type)) out.push(bytes.subarray(i, end));
      i = end;
      if (type === 'IEND') break;
    }
    return new Blob(out, { type: 'image/png' });
  }

  // ── canvas re-encoding ─────────────────────────────────────────────────

  async function reencode(file, { maxEdge, type, quality }) {
    // from-image applies the EXIF rotation, so the result is upright
    // without needing the tag.
    const bmp = await createImageBitmap(file, { imageOrientation: 'from-image' });
    let w = bmp.width, h = bmp.height;
    if (maxEdge && Math.max(w, h) > maxEdge) {
      const k = maxEdge / Math.max(w, h);
      w = Math.round(w * k); h = Math.round(h * k);
    }
    const canvas = document.createElement('canvas');
    canvas.width = w; canvas.height = h;
    canvas.getContext('2d').drawImage(bmp, 0, 0, w, h);
    bmp.close && bmp.close();
    const blob = await new Promise((res) => canvas.toBlob(res, type, quality));
    if (!blob) throw new Error('this browser cannot encode ' + type);
    return blob;
  }

  async function processImage(file, opts, progress) {
    const t = file.type.toLowerCase();
    let out = file;
    let note = [];
    if (opts.compress) {
      progress('compressing image...');
      const target = t === 'image/jpeg' ? 'image/jpeg' : 'image/webp';
      const small = await reencode(file, { maxEdge: MAX_EDGE, type: target, quality: QUALITY });
      if (small.size < file.size) {
        out = small; // re-encoding through a canvas carries no metadata
        note.push('compressed');
        note.push('metadata removed');
        return { blob: out, note };
      }
      note.push('already small; not compressed');
    }
    if (opts.strip) {
      progress('removing metadata...');
      const bytes = new Uint8Array(await file.arrayBuffer());
      let clean = null;
      if (t === 'image/jpeg' && jpegOrientation(bytes) === 1) clean = stripJpeg(bytes);
      else if (t === 'image/png') clean = stripPng(bytes);
      if (!clean) {
        const type = IMAGE_ENCODE[t] || 'image/png';
        clean = await reencode(file, { type, quality: 0.95 });
      }
      out = clean;
      note.push('metadata removed');
    }
    return { blob: out, note };
  }

  // ── ffmpeg.wasm for video and audio ────────────────────────────────────

  let ffmpegPromise = null;

  function loadScript(src) {
    return new Promise((res, rej) => {
      const s = document.createElement('script');
      s.src = src; s.onload = res; s.onerror = () => rej(new Error('could not load ' + src));
      document.head.appendChild(s);
    });
  }

  function getFFmpeg(progress) {
    if (!ffmpegPromise) {
      ffmpegPromise = (async () => {
        progress('loading the video tools (about 32 MB, once)...');
        if (!window.FFmpegWASM) await loadScript('/scripts/vendor/ffmpeg/ffmpeg.js');
        const ff = new window.FFmpegWASM.FFmpeg();
        const base = window.location.origin + '/scripts/vendor/ffmpeg/core/';
        await ff.load({ coreURL: base + 'ffmpeg-core.js', wasmURL: base + 'ffmpeg-core.wasm' });
        return ff;
      })().catch((e) => { ffmpegPromise = null; throw e; });
    }
    return ffmpegPromise;
  }

  function extOf(file) {
    const m = /\.([a-z0-9]{1,5})$/i.exec(file.name || '');
    if (m) return m[1].toLowerCase();
    const t = (file.type || '').split('/')[1] || 'bin';
    return { quicktime: 'mov', 'x-matroska': 'mkv', mpeg: 'mp3' }[t] || t;
  }

  async function processAV(file, opts, progress) {
    const ff = await getFFmpeg(progress);
    const ext = extOf(file);
    const input = 'in.' + ext;
    await ff.writeFile(input, new Uint8Array(await file.arrayBuffer()));
    const onProg = ({ progress: p }) => {
      if (p >= 0 && p <= 1) progress((opts.compress ? 'compressing' : 'removing metadata') + '... ' + Math.round(p * 100) + '%');
    };
    ff.on('progress', onProg);
    // No metadata of any kind: global, per stream, chapters; and no encoder
    // tag written back.
    const clean = ['-map_metadata', '-1', '-map_metadata:s', '-1', '-map_chapters', '-1', '-fflags', '+bitexact', '-flags:v', '+bitexact', '-flags:a', '+bitexact'];
    let output, type, args;
    if (opts.compress && kind(file) === 'video') {
      output = 'out.mp4'; type = 'video/mp4';
      args = ['-i', input, ...clean, '-vf', "scale='min(1280,iw)':-2", '-c:v', 'libx264', '-preset', 'veryfast', '-crf', '28', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-b:a', '96k', '-movflags', '+faststart', output];
    } else {
      output = 'out.' + ext; type = file.type;
      args = ['-i', input, '-map', '0', ...clean, '-c', 'copy'];
      if (['mp4', 'mov', 'm4a', 'm4v'].includes(ext)) args.push('-movflags', '+faststart');
      args.push(output);
    }
    progress(opts.compress ? 'compressing...' : 'removing metadata...');
    try {
      const code = await ff.exec(args);
      if (code !== 0) throw new Error('ffmpeg could not process this file');
      const data = await ff.readFile(output);
      const blob = new Blob([data], { type });
      const note = ['metadata removed'];
      if (opts.compress) {
        if (blob.size >= file.size) return { blob: file, note: ['already small; not compressed', 'metadata kept'] };
        note.unshift('compressed');
      }
      return { blob, note };
    } finally {
      ff.off('progress', onProg);
      try { await ff.deleteFile(input); } catch (_) { /* gone */ }
      try { await ff.deleteFile(output); } catch (_) { /* never made */ }
    }
  }

  // process returns { blob, note } where note lists what was done.
  async function process(file, opts, progress) {
    progress = progress || function () {};
    if (!opts.strip && !opts.compress) return { blob: file, note: [] };
    const k = kind(file);
    if (k === 'image') return processImage(file, opts, progress);
    if (k === 'video' || k === 'audio') return processAV(file, { strip: true, compress: opts.compress }, progress);
    return { blob: file, note: [] };
  }

  window.MediaProcess = { kind, canStrip, canCompress, process };
})();

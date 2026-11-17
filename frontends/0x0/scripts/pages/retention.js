// retention.js — the live retention curve and storage gauge.
//
// The server picks each upload's lifetime so its disk holds steady near a
// target: lifetimes stretch when it is quiet and shrink when it is busy.
// /api/v1/storage publishes the current curve (lifetime by file size), how
// full the store is, and a week of history. This draws it and refreshes it
// every minute. mark(size) highlights where a chosen file sits on the curve.

(function () {
  'use strict';

  const SVG = 'http://www.w3.org/2000/svg';
  const DAY = 86400;
  const $ = (id) => document.getElementById(id);

  let state = null;
  let marked = 0;

  function fmtBytes(n) {
    if (n >= 1099511627776) return (n / 1099511627776).toFixed(1).replace(/\.0$/, '') + ' TB';
    if (n >= 1073741824) return (n / 1073741824).toFixed(1).replace(/\.0$/, '') + ' GB';
    if (n >= 1048576) return (n / 1048576).toFixed(1).replace(/\.0$/, '') + ' MB';
    if (n >= 1024) return (n / 1024).toFixed(0) + ' KB';
    return n + ' B';
  }

  function fmtDuration(sec) {
    if (sec >= 2 * DAY) return Math.round(sec / DAY) + ' days';
    if (sec >= DAY) return '1 day';
    if (sec >= 7200) return Math.round(sec / 3600) + ' hours';
    return Math.max(1, Math.round(sec / 60)) + ' min';
  }

  function el(name, attrs, text) {
    const n = document.createElementNS(SVG, name);
    for (const k in attrs) n.setAttribute(k, attrs[k]);
    if (text != null) n.textContent = text;
    return n;
  }

  // lifetimeFor mirrors the server: clamp(theta * (s_ref/s)^alpha, min, max).
  function lifetimeFor(size) {
    if (!state) return 0;
    const s = Math.max(size, 1024);
    const t = state.theta * Math.pow(state.shape.s_ref / s, state.shape.alpha);
    return Math.max(state.ttl_min, Math.min(state.ttl_max, t));
  }

  function drawCurve() {
    const svg = $('ret-curve');
    if (!svg || !state) return;
    svg.textContent = '';
    // Draw in the pixels the chart actually gets, so text stays readable on
    // a phone instead of shrinking with a fixed-size drawing.
    const W = Math.max(300, Math.round(svg.getBoundingClientRect().width) || 960);
    const H = W < 600 ? 230 : 300, L = 44, R = 12, T = 34, B = 28;
    svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
    const narrow = W < 600;
    const sizeMax = state.size_max;
    const maxDays = Math.max(1, Math.ceil(Math.max(...state.curve.map((p) => p.retention)) / DAY));
    const yMax = niceCeil(maxDays);
    const x = (s) => L + (W - L - R) * (s / sizeMax);
    const y = (d) => T + (H - T - B) * (1 - d / yMax);

    // axes and grid
    svg.appendChild(el('line', { x1: L, y1: H - B, x2: W - R, y2: H - B, class: 'axis' }));
    svg.appendChild(el('line', { x1: L, y1: T, x2: L, y2: H - B, class: 'axis' }));
    for (let k = 0; k <= 4; k++) {
      const d = (yMax * k) / 4;
      svg.appendChild(el('line', { x1: L, y1: y(d), x2: W - R, y2: y(d), class: 'grid' }));
      svg.appendChild(el('text', { x: L - 6, y: y(d) + 4, 'text-anchor': 'end' }, String(Math.round(d))));
    }
    for (let k = 0; k <= 4; k += narrow ? 2 : 1) {
      const s = (sizeMax * k) / 4;
      svg.appendChild(el('text', { x: x(s), y: H - B + 16, 'text-anchor': k === 0 ? 'start' : k === 4 ? 'end' : 'middle' }, fmtBytes(s).replace(' ', '')));
    }
    svg.appendChild(el('text', { x: L - 6, y: T - 16, 'text-anchor': 'end', class: 'label' }, 'days'));

    // the curve, sampled finely on a linear axis
    let d = '';
    for (let i = 0; i <= 120; i++) {
      const s = Math.max(1024, (sizeMax * i) / 120);
      const days = lifetimeFor(s) / DAY;
      d += (i ? 'L' : 'M') + x(s).toFixed(1) + ',' + y(days).toFixed(1);
    }
    svg.appendChild(el('path', { d, class: 'curve' }));

    if (marked > 0) {
      const s = Math.min(marked, sizeMax);
      const days = lifetimeFor(s) / DAY;
      svg.appendChild(el('line', { x1: x(s), y1: y(days), x2: x(s), y2: H - B, class: 'mark' }));
      svg.appendChild(el('circle', { cx: x(s), cy: y(days), r: 4, class: 'dot' }));
      const right = x(s) > W * 0.7;
      const below = y(days) < T + 24; // no room above: put the label under the dot
      svg.appendChild(el('text', { x: x(s) + (right ? -10 : 10), y: y(days) + (below ? 20 : -10), 'text-anchor': right ? 'end' : 'start', class: 'you' }, 'your file: ' + fmtDuration(lifetimeFor(s))));
    }
  }

  function niceCeil(v) {
    for (const n of [7, 14, 30, 60, 90, 120, 180, 240, 365, 730]) if (v <= n) return n;
    return Math.ceil(v / 100) * 100;
  }

  function drawExamples() {
    const pre = $('ret-examples');
    if (!pre || !state) return;
    const max = state.size_max;
    const rows = [
      ['tiny file', 1 << 20],
      ['photo', 4 << 20],
      ['medium', 10 << 20],
      ['large', Math.round(max * 0.6)],
      ['max', max],
    ].filter(([, s]) => s <= max);
    pre.textContent = rows.map(([name, s]) =>
      '  ' + (name + ' (' + fmtBytes(s) + ')').padEnd(22) + (s === max ? '= ' : '≈ ') + fmtDuration(lifetimeFor(s))
    ).join('\n');
  }

  function drawStorage() {
    if (!state) return;
    const pct = (f) => (f * 100).toFixed(f < 0.1 ? 1 : 0) + '%';
    $('ret-used').style.width = Math.min(100, state.fill * 100) + '%';
    $('ret-target').style.left = state.target_fill * 100 + '%';
    $('ret-hard').style.left = state.hard_fill * 100 + '%';
    const lines = [
      '  ' + fmtBytes(state.used) + ' of ' + fmtBytes(state.capacity) + ' in use (' + pct(state.fill) + ')',
      '  steering toward ' + pct(state.target_fill),
      '  uploads refused above ' + pct(state.hard_fill),
      '  incoming ≈ ' + fmtBytes(state.inflow_bytes_per_day) + ' a day',
    ];
    $('ret-storage').textContent = lines.join('\n');

    const svg = $('ret-hist');
    svg.textContent = '';
    const h = state.history || [];
    if (h.length > 1) {
      svg.style.display = 'block';
      const W = 960, H = 50, t0 = h[0].t, t1 = h[h.length - 1].t || t0 + 1;
      const x = (t) => (W * (t - t0)) / Math.max(1, t1 - t0);
      const y = (f) => H - 2 - (H - 4) * Math.min(1, f);
      svg.appendChild(el('line', { x1: 0, x2: W, y1: y(state.target_fill), y2: y(state.target_fill), class: 'target' }));
      let d = '';
      h.forEach((p, i) => { d += (i ? 'L' : 'M') + x(p.t).toFixed(1) + ',' + y(p.fill).toFixed(1); });
      svg.appendChild(el('path', { d, class: 'curve' }));
      const span = t1 - t0;
      $('ret-hist-label').textContent = 'fill, last ' + fmtDuration(span) + ' (dashed: target)';
    } else {
      svg.style.display = 'none';
      $('ret-hist-label').textContent = 'fill history builds up as the server runs';
    }
    $('ret-updated').textContent = 'live · updated ' + new Date(state.updated_at * 1000).toLocaleTimeString();
  }

  function render() {
    drawCurve();
    drawExamples();
    drawStorage();
  }

  async function refresh() {
    try {
      const r = await fetch('/api/v1/storage', { cache: 'no-store' });
      if (!r.ok) throw new Error('HTTP ' + r.status);
      state = await r.json();
      $('ret').style.display = 'block'; // shown first so the chart can measure its width
      render();
    } catch (e) {
      const u = $('ret-updated');
      if (u) u.textContent = 'live curve unavailable (' + e.message + ')';
    }
  }

  window.RetentionView = {
    lifetimeFor,
    fmtDuration,
    mark(size) { marked = size || 0; drawCurve(); },
    get state() { return state; },
    refresh,
  };

  refresh();
  setInterval(refresh, 60000);
  let resizeTimer = null;
  window.addEventListener('resize', () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(drawCurve, 150); });
})();

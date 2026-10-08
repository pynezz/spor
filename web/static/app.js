'use strict';
(() => {
  const B = document.body;
  const root = document.documentElement;
  root.classList.add('js');
  const S = JSON.parse(B.dataset.strings || '{}');
  const $ = (id) => document.getElementById(id);
  const page = B.dataset.page;
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch { /* private mode */ } },
  };

  const fmt = (s) => {
    s = isFinite(s) && s > 0 ? Math.floor(s) : 0;
    return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0');
  };

  let toastTimer;
  function toast(msg) {
    const t = $('toast');
    t.textContent = msg;
    t.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { t.hidden = true; }, 2400);
  }

  /* ---------- service worker ---------- */
  const hasSW = 'serviceWorker' in navigator && B.dataset.sw;
  if (hasSW) {
    navigator.serviceWorker.register(B.dataset.sw, { scope: '/' }).catch(() => {});
    navigator.serviceWorker.addEventListener('message', (e) => {
      if (e.data && e.data.type === 'cached') markOffline(e.data.urls || []);
    });
  }
  async function swPost(msg) {
    if (!hasSW) return;
    try {
      const reg = await navigator.serviceWorker.ready;
      const w = navigator.serviceWorker.controller || reg.active;
      if (w) w.postMessage(msg);
    } catch { /* no worker, no offline cache */ }
  }

  /* ---------- state ---------- */
  let list = [];
  let cur = -1;
  let track = null;
  let peaks = null;
  let peakMax = 1;
  let counted = false;
  let dragPos = null;
  let videoView = true;
  let accent = '#f5a65b';

  const player = $('player');
  const canvas = $('pl-canvas');
  const ctx = canvas.getContext('2d');
  const playBtn = $('pl-play');

  let media;
  if (page === 'share') {
    media = $('media');
    media.removeAttribute('controls');
  } else {
    media = document.createElement('video');
    media.playsInline = true;
    media.preload = 'metadata';
    $('stage').append(media);
  }

  /* ---------- accent colour from cover ---------- */
  function rgb2hsl(r, g, b) {
    r /= 255; g /= 255; b /= 255;
    const mx = Math.max(r, g, b), mn = Math.min(r, g, b);
    let h = 0, s = 0;
    const l = (mx + mn) / 2;
    if (mx !== mn) {
      const d = mx - mn;
      s = l > 0.5 ? d / (2 - mx - mn) : d / (mx + mn);
      if (mx === r) h = (g - b) / d + (g < b ? 6 : 0);
      else if (mx === g) h = (b - r) / d + 2;
      else h = (r - g) / d + 4;
      h /= 6;
    }
    return [h, s, l];
  }
  function hsl2rgb(h, s, l) {
    const f = (n) => {
      const k = (n + h * 12) % 12;
      const a = s * Math.min(l, 1 - l);
      return Math.round(255 * (l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1))));
    };
    return [f(0), f(8), f(4)];
  }
  function accentFrom(url) {
    if (!url) return;
    const img = new Image();
    img.onload = () => {
      const c = document.createElement('canvas');
      c.width = c.height = 16;
      const x = c.getContext('2d', { willReadFrequently: true });
      x.drawImage(img, 0, 0, 16, 16);
      const d = x.getImageData(0, 0, 16, 16).data;
      let r = 0, g = 0, b = 0, n = 0;
      for (let i = 0; i < d.length; i += 4) {
        const mx = Math.max(d[i], d[i + 1], d[i + 2]), mn = Math.min(d[i], d[i + 1], d[i + 2]);
        const w = 1 + ((mx - mn) / 32) ** 2; // favour colourful pixels over grey
        r += d[i] * w; g += d[i + 1] * w; b += d[i + 2] * w; n += w;
      }
      let [h, s, l] = rgb2hsl(r / n, g / n, b / n);
      s = Math.max(s, 0.45);
      l = Math.min(Math.max(l, 0.62), 0.74);
      const [R, G, Bl] = hsl2rgb(h, s, l);
      accent = `rgb(${R}, ${G}, ${Bl})`;
      root.style.setProperty('--accent', accent);
      draw();
    };
    img.src = url;
  }

  /* ---------- waveform ---------- */
  function sizeCanvas() {
    const r = canvas.getBoundingClientRect();
    const d = window.devicePixelRatio || 1;
    canvas.width = Math.max(1, Math.round(r.width * d));
    canvas.height = Math.max(1, Math.round(r.height * d));
    draw();
  }
  function duration() {
    return isFinite(media.duration) && media.duration > 0 ? media.duration : (track && track.duration) || 0;
  }
  function draw() {
    const W = canvas.width, H = canvas.height, d = window.devicePixelRatio || 1;
    ctx.clearRect(0, 0, W, H);
    const dur = duration();
    const pos = dragPos != null ? dragPos : media.currentTime;
    const p = dur ? Math.min(1, pos / dur) : 0;
    const mute = '#3b3f4b';
    if (!peaks) {
      const y = H / 2 - 1.5 * d;
      ctx.fillStyle = mute;
      ctx.fillRect(0, y, W, 3 * d);
      ctx.fillStyle = accent;
      ctx.fillRect(0, y, W * p, 3 * d);
    } else {
      const bw = 2 * d, gap = 1.2 * d;
      const n = Math.max(1, Math.floor(W / (bw + gap)));
      const bins = peaks.length / 2;
      for (let i = 0; i < n; i++) {
        const a = Math.floor((i * bins) / n);
        const b = Math.max(a + 1, Math.floor(((i + 1) * bins) / n));
        let amp = 0;
        for (let j = a; j < b; j++) amp = Math.max(amp, -peaks[2 * j], peaks[2 * j + 1]);
        const h = Math.max(2 * d, (amp / peakMax) * H * 0.94);
        const x = i * (bw + gap);
        ctx.fillStyle = x / W < p ? accent : mute;
        ctx.fillRect(x, (H - h) / 2, bw, h);
      }
    }
    $('pl-cur').textContent = fmt(pos);
    $('pl-dur').textContent = fmt(dur);
    canvas.setAttribute('aria-valuenow', String(Math.round(p * 100)));
    canvas.setAttribute('aria-valuetext', fmt(pos) + ' / ' + fmt(dur));
  }
  let raf = 0;
  function loop() {
    draw();
    raf = media.paused ? 0 : requestAnimationFrame(loop);
  }
  function frac(e) {
    const r = canvas.getBoundingClientRect();
    return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
  }
  canvas.addEventListener('pointerdown', (e) => {
    if (!track) return;
    canvas.setPointerCapture(e.pointerId);
    dragPos = frac(e) * duration();
    draw();
  });
  canvas.addEventListener('pointermove', (e) => {
    if (dragPos == null) return;
    dragPos = frac(e) * duration();
    draw();
  });
  const endDrag = () => {
    if (dragPos == null) return;
    if (duration()) media.currentTime = dragPos;
    dragPos = null;
    draw();
  };
  canvas.addEventListener('pointerup', endDrag);
  canvas.addEventListener('pointercancel', () => { dragPos = null; draw(); });
  new ResizeObserver(sizeCanvas).observe(canvas);

  async function loadPeaks(t) {
    peaks = null;
    peakMax = 1;
    if (!t.peaks) return draw();
    try {
      const r = await fetch(t.peaks);
      if (!r.ok) throw new Error(r.status);
      const p = new Int8Array(await r.arrayBuffer());
      if (track !== t) return;
      for (const v of p) peakMax = Math.max(peakMax, Math.abs(v));
      peaks = p;
    } catch { /* waveform stays a bar */ }
    draw();
  }

  /* ---------- playback ---------- */
  function pickSource(t) {
    if (t.video) return t.video.url;
    const ok = t.audio.find((a) => media.canPlayType(a.type.split(';')[0]) !== '');
    return (ok || t.audio[t.audio.length - 1]).url;
  }

  function sub(t) {
    return [t.style, t.date, t.duration ? fmt(t.duration) : ''].filter(Boolean).join(' · ');
  }

  function load(i, autoplay) {
    const t = list[i];
    if (!t) return;
    cur = i;
    track = t;
    counted = false;
    if (page !== 'share') {
      media.src = pickSource(t);
      media.load();
    }
    player.hidden = false;
    $('pl-title').textContent = t.title;
    $('pl-sub').textContent = sub(t);
    const img = $('pl-img');
    if (t.coverSm) img.src = t.coverSm; else img.removeAttribute('src');
    $('pl-lyrics').hidden = !t.lyrics;
    $('lyrics-text').textContent = t.lyrics || '';
    if (!t.lyrics) $('lyrics').hidden = true;
    $('pl-video').hidden = !t.video;
    setVideoView(t.video ? videoView : false);
    document.querySelectorAll('.card').forEach((c) => c.classList.toggle('current', +c.dataset.i === i));
    accentFrom(t.coverSm);
    loadPeaks(t);
    session(t);
    if (autoplay) play();
    sizeCanvas();
  }

  function play() {
    const p = media.play();
    if (p) p.catch(() => {});
  }
  function toggle() {
    if (!track) return;
    if (media.paused) play(); else media.pause();
  }
  function next() { if (list.length > 1) load((cur + 1) % list.length, true); }
  function prev() {
    if (media.currentTime > 3 || list.length < 2) { media.currentTime = 0; return; }
    load((cur - 1 + list.length) % list.length, true);
  }
  function seekBy(d) {
    const dur = duration();
    media.currentTime = Math.min(Math.max(0, media.currentTime + d), dur || Infinity);
  }

  function setPlaying(on) {
    player.classList.toggle('playing', on);
    playBtn.setAttribute('aria-label', on ? S.pause : S.play);
    document.querySelectorAll('.card').forEach((c) => c.classList.toggle('playing', on));
    if ('mediaSession' in navigator) navigator.mediaSession.playbackState = on ? 'playing' : 'paused';
  }

  media.addEventListener('play', () => {
    setPlaying(true);
    if (!raf) raf = requestAnimationFrame(loop);
    // Fetch the whole file into the service worker cache for offline replay.
    if (track) swPost({ type: 'cache', urls: [media.currentSrc, track.coverSm, track.coverLg, track.peaks].filter(Boolean) });
  });
  media.addEventListener('pause', () => { setPlaying(false); draw(); });
  media.addEventListener('ended', () => { setPlaying(false); if (page !== 'share') next(); });
  media.addEventListener('loadedmetadata', draw);
  media.addEventListener('timeupdate', () => {
    if (media.paused) draw();
    positionState();
    if (!counted && track) {
      const dur = duration();
      if (media.currentTime >= Math.min(30, dur ? dur * 0.5 : 30)) {
        counted = true;
        const q = new URL(track.share, location.origin).search;
        try { navigator.sendBeacon('/api/play/' + encodeURIComponent(track.slug) + q); } catch { /* ignore */ }
        const n = (+store.get('spor.plays') || 0) + 1;
        store.set('spor.plays', String(n));
        if (n >= 3 && navigator.storage && navigator.storage.persist) navigator.storage.persist().catch(() => {});
      }
    }
  });

  playBtn.addEventListener('click', toggle);
  $('pl-next').addEventListener('click', next);
  $('pl-prev').addEventListener('click', prev);
  if (page === 'share') { $('pl-next').hidden = true; $('pl-prev').hidden = true; }

  /* ---------- media session ---------- */
  function session(t) {
    if (!('mediaSession' in navigator)) return;
    const art = [];
    if (t.coverSm) art.push({ src: t.coverSm, sizes: '320x320', type: 'image/jpeg' });
    if (t.coverLg) art.push({ src: t.coverLg, sizes: '1200x1200', type: 'image/jpeg' });
    navigator.mediaSession.metadata = new MediaMetadata({ title: t.title, artist: t.style || S.siteName, album: S.siteName, artwork: art });
    const ms = navigator.mediaSession;
    const set = (a, f) => { try { ms.setActionHandler(a, f); } catch { /* unsupported */ } };
    set('play', play);
    set('pause', () => media.pause());
    set('previoustrack', list.length > 1 ? prev : null);
    set('nexttrack', list.length > 1 ? next : null);
    set('seekbackward', (d) => seekBy(-(d.seekOffset || 10)));
    set('seekforward', (d) => seekBy(d.seekOffset || 10));
    set('seekto', (d) => { media.currentTime = d.seekTime; });
  }
  let lastPos = 0;
  function positionState() {
    const now = Date.now();
    if (now - lastPos < 1000 || !('mediaSession' in navigator) || !navigator.mediaSession.setPositionState) return;
    lastPos = now;
    const dur = duration();
    if (!dur) return;
    try {
      navigator.mediaSession.setPositionState({ duration: dur, playbackRate: media.playbackRate, position: Math.min(media.currentTime, dur) });
    } catch { /* ignore */ }
  }

  /* ---------- video and lyrics ---------- */
  let shareCover = null;
  function setVideoView(on) {
    videoView = on;
    $('pl-video').classList.toggle('on', on);
    $('pl-video').setAttribute('aria-pressed', String(on));
    if (page === 'share') {
      if (!track || !track.video) return;
      if (!shareCover && track.coverLg) {
        shareCover = new Image();
        shareCover.className = 'hero-cover';
        shareCover.alt = '';
        shareCover.src = track.coverLg;
        shareCover.addEventListener('click', toggle);
        $('stage').append(shareCover);
      }
      media.hidden = !on && !!shareCover;
      if (shareCover) shareCover.hidden = on;
    } else {
      $('stage').hidden = !on;
    }
  }
  $('pl-video').addEventListener('click', () => {
    setVideoView(!videoView);
    if (videoView && page !== 'share') $('stage').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  });
  $('pl-cover').addEventListener('click', () => {
    if (track && track.video) $('pl-video').click();
    else if (page === 'share') window.scrollTo({ top: 0, behavior: 'smooth' });
    else document.querySelector('.card.current')?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  });
  $('pl-lyrics').addEventListener('click', () => {
    const l = $('lyrics');
    l.hidden = !l.hidden;
    $('pl-lyrics').classList.toggle('on', !l.hidden);
  });
  $('lyrics-close').addEventListener('click', () => { $('lyrics').hidden = true; $('pl-lyrics').classList.remove('on'); });

  /* ---------- sharing ---------- */
  async function share(t) {
    if (!t) return;
    const url = new URL(t.share, location.origin).href;
    if (navigator.share) {
      try { await navigator.share({ title: t.title, url }); return; } catch (e) { if (e.name === 'AbortError') return; }
    }
    try { await navigator.clipboard.writeText(url); toast(S.copied); } catch { toast(url); }
  }
  $('pl-share').addEventListener('click', () => share(track));

  /* ---------- keyboard ---------- */
  document.addEventListener('keydown', (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || !track) return;
    const tag = e.target.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || e.target.isContentEditable) return;
    switch (e.key) {
      case ' ':
        if (tag === 'BUTTON' || tag === 'SUMMARY' || tag === 'A') return;
        e.preventDefault(); toggle(); break;
      case 'ArrowLeft': e.preventDefault(); seekBy(-5); break;
      case 'ArrowRight': e.preventDefault(); seekBy(5); break;
      case 'j': case 'J': next(); break;
      case 'k': case 'K': prev(); break;
      case 'Escape': $('lyrics').hidden = true; $('pl-lyrics').classList.remove('on'); break;
      default:
    }
  });

  /* ---------- library page ---------- */
  function markOffline(urls) {
    const set = new Set(urls.map((u) => new URL(u, location.origin).pathname));
    list.forEach((t, i) => {
      const srcs = [...t.audio, t.video].filter(Boolean).map((s) => s.url);
      if (srcs.some((u) => set.has(u))) document.querySelector(`.card[data-i="${i}"]`)?.classList.add('offline');
    });
  }

  function card(t, i) {
    const li = document.createElement('li');
    li.className = 'card';
    li.dataset.i = i;
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.setAttribute('aria-label', `${S.play}: ${t.title}`);
    const art = document.createElement('div');
    art.className = 'art';
    if (t.coverSm) {
      const img = new Image();
      img.alt = '';
      img.loading = 'lazy';
      img.decoding = 'async';
      img.src = t.coverSm;
      art.append(img);
    } else {
      const ph = document.createElement('div');
      ph.className = 'ph';
      ph.textContent = (t.title[0] || '♪').toUpperCase();
      art.append(ph);
    }
    if (t.video) {
      const badge = document.createElement('div');
      badge.className = 'badge';
      const s = document.createElement('span');
      s.textContent = S.video;
      badge.append(s);
      art.append(badge);
    }
    const eq = document.createElement('div');
    eq.className = 'eq';
    eq.append(document.createElement('i'), document.createElement('i'), document.createElement('i'));
    art.append(eq);
    const h = document.createElement('h3');
    h.textContent = t.title;
    const p = document.createElement('p');
    p.textContent = sub(t);
    btn.append(art, h, p);
    btn.addEventListener('click', () => (i === cur ? toggle() : load(i, true)));
    li.append(btn);
    return li;
  }

  async function initLibrary() {
    const grid = $('grid');
    try {
      const r = await fetch('/api/library.json');
      if (!r.ok) throw new Error(r.status);
      list = (await r.json()).tracks || [];
    } catch {
      $('empty').textContent = S.loadError;
      $('empty').hidden = false;
      return;
    }
    $('empty').hidden = list.length > 0;
    grid.replaceChildren(...list.map(card));
    const want = decodeURIComponent(location.hash.slice(1));
    const i = list.findIndex((t) => t.slug === want);
    if (i >= 0) load(i, false);
    swPost({ type: 'which', urls: list.flatMap((t) => [...t.audio, t.video].filter(Boolean).map((s) => s.url)) });
  }

  /* ---------- share page ---------- */
  function hex2bytes(h) {
    const out = new Uint8Array(h.length / 2);
    for (let i = 0; i < out.length; i++) out[i] = parseInt(h.substr(i * 2, 2), 16);
    return out;
  }
  async function verify(t) {
    const el = $('verify');
    if (!window.crypto || !crypto.subtle) return;
    el.hidden = false;
    el.textContent = S.verifying;
    let key, sig, signed;
    try {
      const [sigText, keys] = await Promise.all([
        fetch(t.sig).then((r) => (r.ok ? r.text() : Promise.reject(new Error(r.status)))),
        fetch('/api/keys').then((r) => r.text()),
      ]);
      const at = sigText.lastIndexOf('\nmac ');
      signed = sigText.slice(0, at + 1);
      sig = hex2bytes(sigText.slice(at + 5).trim());
      const id = /^key (\S+)$/m.exec(signed)[1];
      const line = keys.split('\n').map((l) => l.trim().split(/\s+/)).find((f) => f[0] === 'ed25519' && f[2] === id);
      const raw = Uint8Array.from(atob(line[1]), (c) => c.charCodeAt(0));
      key = await crypto.subtle.importKey('raw', raw, { name: 'Ed25519' }, false, ['verify']);
    } catch {
      el.hidden = true; // no Ed25519 in this browser, or offline
      return;
    }
    let ok = false;
    try {
      ok = await crypto.subtle.verify({ name: 'Ed25519' }, key, sig, new TextEncoder().encode(signed));
      const listed = new Set([...signed.matchAll(/^sha256 ([0-9a-f]{64}) {2}/gm)].map((m) => m[1]));
      const served = [...t.audio, t.video].filter(Boolean).map((s) => s.url.slice(3, 67));
      ok = ok && served.every((h) => listed.has(h));
    } catch { ok = false; }
    el.textContent = ok ? '✓ ' + S.verified : '✕ ' + S.unverified;
    el.classList.add(ok ? 'ok' : 'bad');
  }

  function initShare() {
    const t = JSON.parse(B.dataset.track);
    list = [t];
    load(0, false);
    const hc = $('hero-cover');
    if (hc) hc.addEventListener('click', toggle);
    if (!t.video) media.addEventListener('click', toggle);
    $('share-btn').hidden = false;
    $('share-btn').addEventListener('click', () => share(t));
    if (window.QR && $('qr-dialog').showModal) {
      $('qr-btn').hidden = false;
      $('qr-btn').addEventListener('click', () => {
        window.QR.draw($('qr-canvas'), new URL(t.share, location.origin).href);
        $('qr-dialog').showModal();
      });
      $('qr-close').addEventListener('click', () => $('qr-dialog').close());
    }
    verify(t);
  }

  if (page === 'share') initShare(); else initLibrary();
})();

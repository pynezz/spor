'use strict';
// spor service worker: app shell precache, offline replay of played tracks.
// Full media files are stored in the Cache API and Range requests are served
// by slicing the cached blob into a synthesized 206, because the Cache API
// cannot hold partial responses.

const params = new URL(self.location.href).searchParams;
const V = params.get('v') || 'dev';
const LIMIT = (Number(params.get('l')) || 500) * 1024 * 1024;
const SHELL = 'spor-shell-' + V;
const MEDIA = 'spor-media';
const ASSETS = 'spor-assets';
const PAGES = 'spor-pages';
const LRU_KEY = '/__spor/lru';

self.addEventListener('install', (e) => {
  e.waitUntil(
    caches.open(SHELL)
      .then((c) => c.addAll(['app.js', 'qr.js', 'app.css', 'icon.svg', 'app.webmanifest',
        'fonts/inter-400.woff2', 'fonts/inter-500.woff2', 'fonts/inter-600.woff2', 'fonts/inter-tight-600.woff2', 'fonts/inter-tight-700.woff2'].map((f) => `/static/${V}/${f}`)))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (e) => {
  e.waitUntil((async () => {
    for (const k of await caches.keys()) {
      if (k.startsWith('spor-shell-') && k !== SHELL) await caches.delete(k);
    }
    await self.clients.claim();
  })());
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  const p = url.pathname;
  if (p.startsWith('/m/')) e.respondWith(media(req, url));
  else if (p.startsWith('/c/') || p.startsWith('/p/')) e.respondWith(cacheFirst(req, ASSETS));
  else if (p.startsWith('/static/')) e.respondWith(cacheFirst(req, SHELL));
  else if (p === '/' || p.startsWith('/t/') || p === '/api/library.json' || p.startsWith('/api/sig/') || p === '/api/keys') {
    e.respondWith(networkFirst(req));
  }
});

async function cacheFirst(req, name) {
  const c = await caches.open(name);
  const hit = await c.match(req, { ignoreVary: true });
  if (hit) return hit;
  const res = await fetch(req);
  if (res.ok && res.status === 200) c.put(req, res.clone()).catch(() => {});
  return res;
}

async function networkFirst(req) {
  const c = await caches.open(PAGES);
  try {
    const res = await fetch(req);
    if (res.ok) c.put(req, res.clone()).catch(() => {});
    return res;
  } catch (err) {
    const hit = await c.match(req, { ignoreVary: true });
    if (hit) return hit;
    throw err;
  }
}

async function media(req, url) {
  const key = url.origin + url.pathname;
  const c = await caches.open(MEDIA);
  const hit = await c.match(key);
  if (!hit) return fetch(req);
  touch(key);
  return ranged(hit, req.headers.get('range'));
}

async function ranged(res, range) {
  const blob = await res.blob();
  const size = blob.size;
  const type = res.headers.get('content-type') || 'application/octet-stream';
  const base = { 'Content-Type': type, 'Accept-Ranges': 'bytes', 'Cache-Control': 'no-store' };
  const m = range && /^bytes=(\d*)-(\d*)$/.exec(range.trim());
  if (!m || (m[1] === '' && m[2] === '')) {
    return new Response(blob, { status: 200, headers: { ...base, 'Content-Length': String(size) } });
  }
  let start, end;
  if (m[1] === '') {
    start = Math.max(0, size - Number(m[2]));
    end = size - 1;
  } else {
    start = Number(m[1]);
    end = m[2] === '' ? size - 1 : Math.min(Number(m[2]), size - 1);
  }
  if (start >= size || start > end) {
    return new Response(null, { status: 416, headers: { ...base, 'Content-Range': `bytes */${size}` } });
  }
  return new Response(blob.slice(start, end + 1, type), {
    status: 206,
    headers: { ...base, 'Content-Range': `bytes ${start}-${end}/${size}`, 'Content-Length': String(end - start + 1) },
  });
}

// LRU bookkeeping, serialized so concurrent updates do not clobber each other.
let queue = Promise.resolve();
const serial = (fn) => (queue = queue.then(fn, fn));

async function readLRU() {
  const c = await caches.open(PAGES);
  const r = await c.match(LRU_KEY);
  try { return r ? await r.json() : {}; } catch { return {}; }
}
async function writeLRU(lru) {
  const c = await caches.open(PAGES);
  await c.put(LRU_KEY, new Response(JSON.stringify(lru), { headers: { 'Content-Type': 'application/json' } }));
}
function touch(key) {
  serial(async () => {
    const lru = await readLRU();
    if (lru[key]) { lru[key].t = Date.now(); await writeLRU(lru); }
  });
}

const hex = (buf) => [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, '0')).join('');

async function store(url) {
  const u = new URL(url, self.location.origin);
  if (u.origin !== self.location.origin || !u.pathname.startsWith('/m/')) {
    // Covers and peaks: just make sure they are in the asset cache.
    if (u.pathname.startsWith('/c/') || u.pathname.startsWith('/p/')) {
      await cacheFirst(new Request(u.href), ASSETS).catch(() => {});
    }
    return false;
  }
  const key = u.origin + u.pathname;
  const c = await caches.open(MEDIA);
  if (await c.match(key)) { touch(key); return true; }
  const res = await fetch(key);
  if (res.status !== 200) return false;
  const buf = await res.arrayBuffer();
  // Content addressing: the URL is the SHA-256 of the bytes. Check it.
  const want = u.pathname.slice(3).split('.')[0];
  if (hex(await crypto.subtle.digest('SHA-256', buf)) !== want) return false;
  if (buf.byteLength > LIMIT) return false;
  await c.put(key, new Response(buf, { headers: { 'Content-Type': res.headers.get('content-type') || '' } }));
  await serial(async () => {
    const lru = await readLRU();
    lru[key] = { size: buf.byteLength, t: Date.now() };
    let total = Object.values(lru).reduce((a, x) => a + x.size, 0);
    const old = Object.entries(lru).sort((a, b) => a[1].t - b[1].t);
    for (const [k, v] of old) {
      if (total <= LIMIT || k === key) continue;
      await c.delete(k);
      delete lru[k];
      total -= v.size;
    }
    await writeLRU(lru);
  });
  return true;
}

async function cachedOf(urls) {
  const c = await caches.open(MEDIA);
  const out = [];
  for (const url of urls) {
    const u = new URL(url, self.location.origin);
    if (await c.match(u.origin + u.pathname)) out.push(u.pathname);
  }
  return out;
}

self.addEventListener('message', (e) => {
  const d = e.data || {};
  const urls = Array.isArray(d.urls) ? d.urls.slice(0, 2000) : [];
  const reply = (list) => e.source && e.source.postMessage({ type: 'cached', urls: list });
  if (d.type === 'cache') {
    e.waitUntil((async () => {
      const done = [];
      for (const u of urls) {
        try { if (await store(u)) done.push(new URL(u, self.location.origin).pathname); } catch { /* offline or quota */ }
      }
      if (done.length) reply(done);
    })());
  } else if (d.type === 'which') {
    e.waitUntil(cachedOf(urls).then(reply));
  }
});

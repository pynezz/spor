# spor: self-hosted music library

A single Go binary that serves my own Suno tracks (audio and video) from a directory, with a small, good-looking web UI and shareable links that work for people without a Suno account.

## Goals

- Share a track with one link. The link works in a browser and previews nicely in Signal, Messenger, Discord and similar apps.
- Drop files into a directory, sign them, done. No database, no admin UI, no accounts.
- Only serve files that carry a valid integrity proof.
- Lean enough to run next to other services on the Hetzner VPS or the NUC.
- Push as much work as possible to the client: caching, waveform rendering, playback state.

## Non-goals

- Multi-user uploads, comments, likes, playlists shared between users.
- On-the-fly transcoding (this is the only thing that would make compute matter).
- DRM. Anyone who can play a track can save it. Accept that.

## Corrections to the original brief

1. **"Client side decompressing" mostly does not apply to the media.** MP3, M4A, Opus and MP4 are already compressed; gzip or brotli on top gains roughly nothing and burns CPU. Compression only helps the text payloads (library manifest, lyrics, waveform peaks). For those, precompress once at index time and serve with `Content-Encoding: br`; the browser decompresses natively, which is faster than any `DecompressionStream` code in JS. The real client-side win is **caching**, see below.
2. **A sidecar per file collides** when a directory holds more than one file under the same sidecar name. Use one directory per track, and make `track.sig` a signed manifest covering every file in that directory (media, cover, lyrics). That also protects the cover and metadata.
3. **Consider Ed25519 instead of HMAC.** With HMAC, the same secret must live on the server and on the machine that signs. With Ed25519 the server only holds the public key, so a compromised server cannot mint valid manifests, and the browser can verify too (WebCrypto supports Ed25519 in current browsers). Both are stdlib in Go and equally fast for this purpose, since the cost is hashing the files, not the MAC or signature. Decided: Ed25519 only (see Decisions).

## Library layout

```
library/
  neon-fjord/
    track.mp3          # or track.mp4 (video), or both
    cover.jpg          # optional; extracted from MP4 first frame if missing
    lyrics.txt         # optional
    meta.toml          # optional: title, date, style, description, visibility
    track.sig          # required; directory is ignored without a valid one
  another-song/
    ...
```

The directory name is the slug and the public URL path (`/t/neon-fjord`).

### `track.sig` format

Plain text, easy to diff and generate:

```
spor-manifest v1
alg ed25519
key 3f9a1c2e
sha256 9b2f...e1  track.mp3
sha256 77ac...0d  cover.jpg
sha256 c41e...9a  meta.toml
mac 4d8a...f3
```

- `mac` is the Ed25519 signature over every line above it, byte for byte. The `alg` line is fixed to `ed25519` and kept only so a future format change is detectable.
- `key` is a key ID so keys can be rotated without re-signing everything at once.
- Files present in the directory but not listed are ignored and logged. Listed files that are missing or mismatched cause the whole track to be rejected.

### Verification cost

- Go's `crypto/sha256` uses SHA-NI on amd64 and the ARMv8 crypto extensions on arm64, so expect around 1.5 to 2 GB/s per core. A 5 MB MP3 hashes in roughly 3 ms; a 60 MB MP4 in roughly 40 ms.
- Verify **once** at scan time, never per request.
- Cache results in `state.json` keyed by `(dev, inode, size, mtime_ns)`. Unchanged files skip hashing on restart, so startup is instant even with hundreds of tracks.
- Hash files in parallel with a worker pool sized to `GOMAXPROCS`.

## Architecture

```
            +---------------------- spor (single binary) -----------------------+
 library/ ->| scanner -> verifier -> index (in-memory, atomic swap) -> HTTP     |
            |    ^                                    |                         |
            |  rescan: SIGHUP, poll every 60 s,       +-> manifest.json(.br)    |
            |  or inotify                              +-> /t/<slug> HTML + OG  |
            |                                          +-> /m/<hash>.<ext> media |
            |  embed.FS: index.html, app.js, app.css, sw.js                      |
            +--------------------------------------------------------------------+
                                       |
                                     Caddy (TLS, HTTP/2/3)
```

### Components

- **Scanner.** Walks `library/`, one level deep. Produces candidate tracks.
- **Verifier.** Parses the manifest, checks the MAC or signature, hashes listed files (or reuses cached hashes). Emits verified tracks only.
- **Index.** Immutable snapshot of verified tracks, swapped with `atomic.Pointer`. Requests never block on a rescan.
- **Ingest artifacts** (computed at scan time, cached under `cache/<sha256>/`):
  - waveform peaks: about 1000 min/max pairs as `int8`, around 2 KB per track. Decode via an optional `ffmpeg` call; skip peaks if `ffmpeg` is absent rather than adding a decoder dependency.
  - duration, from MP3 frame headers or the MP4 `mvhd` box (both parseable without dependencies).
  - cover thumbnails in two sizes, generated with stdlib `image/jpeg` plus a simple resize.
- **HTTP server.** `net/http` only. Routes below.

### Routes

| Route | Purpose | Caching |
|---|---|---|
| `GET /` | Library UI | `no-cache` (revalidate, tiny) |
| `GET /t/<slug>` | Share page, server-rendered HTML with OpenGraph tags | `no-cache` |
| `GET /api/library.json` | Manifest of visible tracks, precompressed br/gzip | `no-cache` + strong ETag |
| `GET /m/<sha256>.<ext>` | Media, content-addressed | `public, max-age=31536000, immutable` |
| `GET /c/<sha256>-<size>.jpg` | Cover art | `immutable` |
| `GET /p/<sha256>.bin` | Waveform peaks | `immutable` |
| `GET /static/<hash>/...` | Embedded UI assets | `immutable` |

Media is addressed by content hash, so the same bytes are cached forever and a re-upload automatically gets a new URL. Paths are resolved through the index only; the request path is never joined onto the filesystem, which removes path traversal as a class.

`http.ServeContent` handles Range requests, `If-None-Match` and `HEAD`, which is everything seeking needs. On Linux, Go's file serving uses `sendfile`, so bytes go from page cache to socket without passing through userspace.

## Sharing

The actual reason this exists, so it gets first-class treatment:

- `/t/<slug>` renders a minimal page with the player and these tags:
  - `og:title`, `og:description`, `og:image` (cover)
  - `og:audio` / `og:video` with type and secure URL
  - `twitter:card=player` where useful
- Share page works without JavaScript (plain `<audio>` / `<video>` element), then upgrades.
- Visibility per track in `meta.toml`:
  - `public`: listed on `/` and reachable by slug.
  - `unlisted`: not listed; reachable only via `/t/<slug>?k=<token>` where the token is 128 random bits stored in `meta.toml`. A bearer capability, no accounts.
  - `draft`: never served.
- Optional: QR code on the share page for in-person sharing (generated client-side).

## Web UI

Vanilla JS and CSS, no framework, no build step. Target: under 40 KB total before compression.

- Dark, cover-forward layout. One accent color derived from the cover art (average of a downscaled cover on a canvas).
- Waveform scrubber drawn on `<canvas>` from the peaks file. Click and drag to seek.
- Video tracks: toggle between video view and cover view; audio keeps playing when switching.
- Media Session API for lock-screen and headphone controls, with cover art.
- Lyrics panel if `lyrics.txt` exists.
- Keyboard: space, arrows, `j`/`k` for next and previous.
- Strict CSP: `default-src 'self'`; no inline scripts, no third-party anything. Fonts from your own CDN on api.pynezz.dev or system fonts.

### Client-side caching

- **HTTP cache** does most of the work thanks to `immutable` content-addressed URLs.
- **Service worker** adds offline replay:
  - Precache the app shell.
  - On play, fetch the full media file (status 200) in the background and store it in the Cache API.
  - Serve subsequent Range requests from the cached blob by slicing it and synthesizing a `206` response. This is needed because the Cache API cannot store partial `206` responses, which is the usual reason audio service workers break seeking.
  - Cap cache size (for example 500 MB, configurable), evict least recently played. Ask for `navigator.storage.persist()` only after the user has played a few tracks.
- `library.json` is fetched with its ETag; a `304` costs almost nothing.
- Optional client-side verification: fetch the manifest, hash cached media with `crypto.subtle.digest('SHA-256', ...)`, and with Ed25519 verify the signature in the browser. Nice proof of integrity, low priority.

## Capacity: 20 concurrent listeners

Compute is a non-issue as long as nothing transcodes per request. The binding constraint is upstream bandwidth.

| Content | Bitrate | 20 listeners |
|---|---|---|
| Opus 128 kbps | 0.128 Mbps | 2.6 Mbps |
| MP3 320 kbps (Suno default) | 0.32 Mbps | 6.4 Mbps |
| WAV 16-bit 44.1 kHz | 1.41 Mbps | 28 Mbps |
| MP4 720p | about 2.5 Mbps | about 50 Mbps |

- CPU: serving static files via `sendfile` at 50 Mbps is a rounding error, low single-digit percent of one core. Go handles thousands of idle connections in a few MB of RAM.
- Memory: index plus goroutines in the 15 to 30 MB range. Media lives in the page cache, which the kernel manages.
- Disk: a library of 200 tracks at about 7 MB each is about 1.4 GB, all of which fits in page cache on either machine.
- Bandwidth: the Hetzner VPS has 1 Gbit and a large traffic allowance, so even the video case is fine. The NUC is limited by residential upload; if that is under about 50 Mbps, host video on the VPS.
- Real-world load is lower than the table suggests: the browser buffers ahead then idles, and repeat plays come from the client cache.

### Recommendation

- Never serve WAV to the browser. If the source is WAV, transcode at ingest (local, on the workstation) to Opus 160 kbps in WebM or MP4 plus an MP3 fallback.
- Keep the Suno MP3 as-is otherwise; transcoding a lossy source to another lossy format only saves bandwidth, it does not improve quality.

## Signing workflow

A tiny companion subcommand in the same binary, run on the workstation:

```
spor sign library/neon-fjord        # writes track.sig
spor sign --all library/            # signs every unsigned or changed track
spor verify library/                # dry run, reports problems
spor keygen                         # new Ed25519 keypair
spor publish library/neon-fjord     # sign, then rsync to the server, then SIGHUP
```

Publishing is then one command from download folder to shareable link. The private key never leaves the workstation when using Ed25519.

## Deployment

- Rootless Podman with a Quadlet unit, behind Caddy.
- Image: `scratch` or distroless static with the single binary (`CGO_ENABLED=0`). Add `ffmpeg` only in the workstation tooling, not the server image.
- Library mounted read-only with `:Z` for SELinux; a separate small writable volume for `cache/` and `state.json`.
- Key material via Podman secret or systemd `LoadCredential`, never in the image or env.
- Quadlet hardening: `ReadOnly=true`, `NoNewPrivileges=true`, `DropCapability=ALL`, `UserNS=auto`, memory limit of 128 MB.
- Caddy handles TLS and HTTP/3. Do not enable `encode` for `/m/*`; keep it for HTML and JSON.
- Per-IP rate limit on `/m/*` (token bucket in the binary, generous enough for seeking) to blunt casual scraping and hotlink abuse.

## Configuration

One small TOML file or flags:

```toml
listen = "127.0.0.1:8740"
library = "/srv/spor/library"
cache = "/srv/spor/cache"
public_url = "https://lytt.txtv.no"
rescan_interval = "60s"
sw_cache_limit_mb = 500
```

## Dependencies

- Stdlib for everything on the server, including Ed25519, SHA-256, HTTP, JPEG, embed.
- One exception worth considering: a TOML parser. Alternative: use `meta.json` and stay pure stdlib. Recommendation: JSON, since it is edited rarely and that keeps the dependency count at zero.
- `ffmpeg` only on the workstation, for peaks and optional transcoding. If peaks should also be generated server-side, compute them in Go from MP3 by shelling out at scan time; failure just disables the waveform.

## Milestones

1. **Core.** Scanner, manifest format, verifier with cached hashes, in-memory index, content-addressed media route with Range support. `spor sign` and `spor verify`.
2. **Share page.** `/t/<slug>` with OpenGraph tags and no-JS fallback. Verify previews in Signal, Messenger and Discord. At this point the Suno problem is solved.
3. **Library UI.** Cover grid, player, Media Session, keyboard controls, accent color.
4. **Waveforms and video.** Peaks at ingest, canvas scrubber, video toggle.
5. **Service worker.** Offline cache with ranged replay from full blobs, eviction, persist request.
6. **Deploy.** Quadlet, Caddy site block, `spor publish`, rate limiting.
7. **Nice to have.** Unlisted tokens, QR codes, browser-side signature verification, a simple play counter written to an append-only log (no cookies, no IP storage).

## Decisions

- **Hosting:** Hetzner VPS. Covers the video bandwidth case without touching residential upload.
- **Integrity:** Ed25519 only. HMAC support is dropped; the private key stays on the workstation, the server and browser get the public key. The manifest file is named `track.sig`.
- **Downloads:** stream only, no download button. The service worker still caches full files for offline replay; that is a client-side cache, not a download feature.
- **Lyrics:** read from `lyrics.txt` only, no automatic import. Video tracks show lyrics burned into the MP4, and the raw text is still offered in the lyrics panel.
- **Domain:** `lytt.txtv.no` is canonical. Share links take the form `https://lytt.txtv.no/t/<slug>`. `musikk.txtv.no` and `tracks.txtv.no` point at the same IP; Caddy answers them with a `308` redirect to the same path on `lytt.txtv.no` instead of serving the site on three hosts. That keeps one origin for the service worker cache, one set of OpenGraph URLs, and avoids three copies of every track in a listener's browser cache.
- **UI language:** Norwegian bokmål (`lang="nb"`) by default, English (`en-US`) as fallback. All UI strings live in embedded `strings/nb.json` and `strings/en-US.json`. The server picks one from `Accept-Language` (any `nb`, `no` or `nn` gets `nb`; anything else gets `en-US`) and sets `Vary: Accept-Language` on HTML responses. OpenGraph tags follow the same choice, with `og:locale` set to `nb_NO` or `en_US`. Track titles, descriptions and lyrics are never translated.

### Caddy sketch

```caddyfile
musikk.txtv.no, tracks.txtv.no {
	redir https://lytt.txtv.no{uri} permanent
}

lytt.txtv.no {
	@text path / /t/* /api/* /static/*
	encode @text zstd gzip
	reverse_proxy 127.0.0.1:8740
}
```

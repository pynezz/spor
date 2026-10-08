# URLs and API

## Pages

| URL | What |
|---|---|
| `/` | the library: featured newest track, all public tracks |
| `/t/<slug>` | share page for one track, with link-preview tags; works without JavaScript |
| `/t/<slug>?k=<token>` | share page for an unlisted track |
| `?lang=en` | English strings on any page; Norwegian is the default for everyone |

## Files

Most file URLs contain a hash. A file's URL changes whenever its content does, so browsers can cache these forever.

| Prefix | What | The hash is |
|---|---|---|
| `/m/<sha256>.<ext>` | the audio or video file, streamed in chunks (Range requests) | SHA-256 of that media file |
| `/c/<sha256>-320.jpg` | small cover: grid and player | SHA-256 of the track's cover file |
| `/c/<sha256>-1200.jpg` | large cover: share page and link previews | same |
| `/p/<sha256>.bin` | waveform peaks (see [track-format.md](track-format.md#peaksbin)) | SHA-256 of the track's **media** file, not of the peaks file |
| `/static/<hash>/…` | the app: CSS, JS, fonts, logo, footer image | hash of all static files together; changes on every release |
| `/sw.js` | service worker for offline playback | |

Example: `/p/bef2086ca4f2…781c.bin` is the waveform for the track whose `track.mp4` has that hash. You can find it with `grep bef2086c library/*/track.sig`.

Media is only served if its bytes still match what was verified at the last scan. A file changed on disk is refused until the next scan.

## API

| Route | What |
|---|---|
| `GET /api/library.json` | all public tracks (gzip, ETag) |
| `GET /api/sig/<slug>` | the track's `track.sig`, for in-browser verification |
| `GET /api/keys` | the server's public keys |
| `POST /api/play/<slug>` | count one play; the player sends it after 30 s (or half the track, if shorter) |
| `POST /api/like/<slug>` | one heart; needs header `X-Spor-Session: <32 hex>`. `200` with the new count, `409` if that session already liked the track, `400` without a valid session, `404` if likes are off. |
| `GET /api/stats` | plays and likes for every public track; `liked` for the session in `X-Spor-Session` |
| `GET /api/stats/<slug>` | the same for one track (add `?k=` for unlisted) |
| `GET /healthz` | `ok` |

Unlisted tracks need `?k=<token>` on every route that names the slug. Plays, likes and media are rate-limited per IP (`media_rate`, `media_burst`).

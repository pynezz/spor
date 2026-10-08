# Track folders and `meta.toml`

Each track is one folder in the library. The folder name is the **slug**: it becomes the link `https://lytt.txtv.no/t/<slug>`.

```
albatrossalt/          # slug: a-z, 0-9, - _ . (max 100 chars, no leading dot or dash)
  track.mp3            # audio: mp3, m4a, aac, opus, ogg, oga, webm, flac
  track.mp4            # video (optional): mp4 or m4v
  cover.jpg            # jpg or png
  lyrics.txt           # optional
  meta.toml            # optional, but you want it (meta.json also works)
  peaks.bin            # waveform, made by `spor sign` when ffmpeg is present
  track.sig            # signed list of every file above
```

Media files must be named `track.<ext>`. A file like `albatrossalt.mp4` is ignored, and `spor sign` fails with "no track.<…> found". Several `track.*` audio files are offered to the browser as alternatives, smallest modern format first and mp3 last.

Files not listed in `track.sig` are ignored, and the server logs a warning about them. A folder without a valid `track.sig` is ignored completely.

## `meta.toml`

```toml
title = "Albatrossalt"
date = "2026-10-08"                      # published: YYYY-MM-DD
created = "2025-06"                      # originally made: YYYY, YYYY-MM or YYYY-MM-DD
style = "countryvise, vise, felespel"    # genre, free text
description = "tross alt?"
visibility = "public"                    # public | unlisted | draft
token = "…"                              # unlisted only; added by `spor sign`
```

| Field | Shown where | Notes |
|---|---|---|
| `title` | everywhere | falls back to the slug |
| `date` | share page ("Lagt ut"); sorting | the library lists newest `date` first; the top track is "Nyeste utgivelse" |
| `created` | cards, featured track, share page ("Laget") | formatted in Norwegian: `2025-06-14` → "14. juni 2025", `2025-06` → "juni 2025" |
| `style` | small label on cards, tag on featured track and share page, "Stil" row | shown as written |
| `description` | cards (two lines), featured track and share page (in full), link previews | |
| `visibility` | | `public`: in the library. `unlisted`: only via `/t/<slug>?k=<token>`. `draft`: nowhere. |
| `token` | | 128-bit random value; `spor sign` adds it to unlisted tracks and prints the link. `spor token` prints a fresh one. |

The format is flat TOML: one `key = "value"` per line, `#` for comments. Inside values, write `\"` for a quote mark and `\n` for a line break.

Any change to `meta.toml` needs a re-sign: `./spor publish library/<slug>`.

## `track.sig`

A plain-text manifest, signed with your private key:

```
spor-manifest v1
alg ed25519
key 98a4e050
sha256 3aa19753…  cover.png
sha256 58f85ace…  meta.toml
sha256 e85393be…  peaks.bin
sha256 bef2086c…  track.mp4
mac 870e5166…
```

- **`key`** is the id of the signing key, so the server knows which public key to check with.
- **Each `sha256` line** is the hash of one file. If any listed file is missing or changed, the whole track is rejected.
- **`mac`** is the Ed25519 signature over everything above it.

See [signing.md](signing.md).

## `peaks.bin`

The waveform drawn in the player: 1000 min/max pairs of signed bytes (2000 bytes), one pair per slice of the track. `spor sign` creates it with ffmpeg and signs it with the rest. If it's missing and ffmpeg is installed on the server, spor makes one itself.

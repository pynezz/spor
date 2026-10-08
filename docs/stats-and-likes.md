# Plays, likes and what is stored

## Plays

- **Counted** when someone has listened for 30 seconds, or half the track if it's shorter than a minute.
- **Stored** in `/var/lib/spor/plays.log`, one line per play: `2026-10-08T15:52:06Z albatrossalt`. No IP, no cookie.
- **Kept across restarts:** the counts are rebuilt from the log at startup. With `play_log = false` plays are still counted, but only in memory.
- **Shown** on cards, the featured track, share pages, and as totals over the library.

## Likes (hearts)

- **One heart per browser per track.**
  - **In the browser:** the button disables as soon as it's clicked, and stays disabled after a reload. The browser remembers which tracks it liked in `localStorage`.
  - **On the server:** each browser keeps a random session id in `localStorage` and sends it with the like (`X-Spor-Session`). A second like from the same session gets `409` and isn't counted.
- **Stored** in `/var/lib/spor/likes.log`: time, slug, and a hash of the session id. No IP, nothing that identifies a person.
- **No protection against determined cheating.** Clearing browser data or using a private window gives a new session, and another heart. That's intended: it stops double clicks, nothing more.
- **Turn off** with `likes = false`. The hearts disappear everywhere.

## Reset or correct counts

Both logs are plain text, so you can edit them while spor is stopped:

```sh
sudo systemctl stop spor
sudo sed -i '/ old-slug$/d' /var/lib/spor/plays.log          # forget plays of one track
sudo truncate -s 0 /var/lib/spor/likes.log                    # reset all likes
sudo systemctl start spor
```

Counts are stored by slug. Renaming a track starts it at zero unless you also rename the slug in both logs.

## What else is stored

| Where | What | Personal data? |
|---|---|---|
| `/var/lib/spor/plays.log` | time, slug | no |
| `/var/lib/spor/likes.log` | time, slug, hashed random session id | no |
| `/var/lib/spor/state.json` | cached file hashes and media info | no |
| spor's memory | IPs for rate limiting, dropped about 10 minutes after their last request | briefly, never written |
| `journalctl -u spor` | startup, scans, warnings; no requests | no |
| `/var/log/caddy/lytt.txtv.no.log` | every request with the **full IP**, user agent and URL | **yes**. See [logging.md](logging.md). |
| the listener's browser | session id, liked tracks, offline media cache | on their device only |

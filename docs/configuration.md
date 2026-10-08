# Configuration reference

## `/etc/spor/spor.toml`

Flat TOML: one `key = value` per line. Unknown keys are an error, so a typo stops spor from starting (check `journalctl -u spor`). Restart after changes: `sudo systemctl restart spor`.

| Key | Default | Meaning |
|---|---|---|
| `listen` | `"127.0.0.1:8740"` | address spor listens on; Caddy proxies to it |
| `library` | `"/srv/spor/library"` | track folders |
| `cache` | `"/var/lib/spor"` in the shipped config | writable state: `plays.log`, `likes.log`, `state.json`, thumbnails |
| `public_url` | `"https://lytt.txtv.no"` | used for link-preview tags and canonical links. Change it when moving domains; no re-signing needed. |
| `public_keys` | unset | public key file(s), comma separated. Unset: spor reads the key systemd hands in (`LoadCredential=`). |
| `rescan_interval` | `"60s"` | how often to rescan the library; `0` turns it off (SIGHUP still works) |
| `sw_cache_limit_mb` | `500` | how much media each browser may keep for offline playback |
| `trust_proxy` | `true` | trust `X-Forwarded-For` from loopback (Caddy) for rate limiting |
| `media_rate` | `10` | per-IP requests per second on media, plays and likes |
| `media_burst` | `60` | per-IP burst allowance |
| `play_log` | `true` | record plays in `plays.log`; off means counts reset on restart |
| `likes` | `true` | hearts on; off hides them everywhere |
| `suno_url` | `"https://suno.com/@skumx"` | footer "Følg @… på Suno" button; the handle is taken from the URL. Empty hides it. |
| `referral_url` | unset | footer referral button and the small note under it. Empty hides both. |

`suno_url` and `referral_url` must be `https://` URLs.

## `spor serve` flags

Flags override the config file, which is `/etc/spor/spor.toml` when present.

| Flag | Overrides |
|---|---|
| `-config FILE` | which config file to read |
| `-listen ADDR` | `listen` |
| `-library DIR` | `library` |
| `-cache DIR` | `cache` |
| `-pub FILES` | `public_keys` |
| `-url URL` | `public_url` |
| `-suno URL` | `suno_url` |
| `-referral URL` | `referral_url` |
| `-debug` | verbose logging |

## Other commands

| Command | Does |
|---|---|
| `spor keygen [-out DIR] [-comment TEXT]` | new Ed25519 keypair |
| `spor sign [-key FILE] [-url URL] [-force] [-ingest=false] DIR` | sign one track |
| `spor sign -all LIBRARY` | sign every new or changed track |
| `spor verify [-pub FILES] DIR\|LIBRARY` | check without changing anything |
| `spor publish [-key FILE] [-remote HOST:PATH] [-reload CMD] [-url URL] DIR` | sign, rsync, reload, print link |
| `spor token` | print a fresh unlisted token |
| `spor version` | print the version |

Environment variables for these are listed in [workstation.md](workstation.md#environment).

# spor documentation

spor is a self-hosted music library in one Go binary. You prepare and **sign** tracks on your workstation, upload them to a server, and spor serves them at `https://lytt.txtv.no` with share pages, plays, likes and offline playback. The server only holds the public key, so it can check tracks but never create them.

```
workstation                                   VPS
───────────                                   ───
song.mp3 ──add-track.sh──▶ library/<slug>/    /srv/spor/library/<slug>/
                           meta.toml          (read-only for spor)
             spor sign ──▶ track.sig  ──rsync──▶      │
             (private key)                            ▼
                                              spor serve (public key)
                                                      │
                                              Caddy (HTTPS) ──▶ listeners
```

## Where to look

| I want to… | Read |
|---|---|
| set up my machine, keys and local testing | [workstation.md](workstation.md) |
| add, change, rename or remove tracks | [publishing.md](publishing.md) |
| know what goes in a track folder and `meta.toml` | [track-format.md](track-format.md) |
| understand keys and what the signature covers | [signing.md](signing.md) |
| install or update the server, Caddy, firewall, DNS | [server.md](server.md) |
| look up a `spor.toml` setting, flag or env variable | [configuration.md](configuration.md) |
| know what `/m/…`, `/c/…`, `/p/…` and the API routes are | [urls-and-api.md](urls-and-api.md) |
| understand play counts, likes and what is stored | [stats-and-likes.md](stats-and-likes.md) |
| read the access log or watch requests live | [logging.md](logging.md) |
| change texts, language, logo, footer or Suno links | [site.md](site.md) |
| fix something that broke | [troubleshooting.md](troubleshooting.md) |

## The two commands you use most

```sh
scripts/add-track.sh -p -s "genre" -D "description" ~/Music/song.mp3   # new track, live in seconds
./spor publish library/<slug>                                         # after editing a track
```

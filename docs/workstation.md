# Workstation setup

Everything that creates or changes tracks happens on your workstation: preparing folders, signing, uploading.

## Requirements

- **Go 1.25 or newer** to build spor. Newer versions are fine; `go.mod` only sets the minimum.
- **ffmpeg and ffprobe**, used by `add-track.sh` (tags, covers, transcoding) and by `spor sign` (waveforms, video cover frames).
- **rsync and ssh**, used by `spor publish` to upload.

## Build

```sh
go build -o spor .
./spor version
```

`scripts/add-track.sh` finds `spor` in this order: `$SPOR_BIN`, `spor` on your `PATH`, then `./spor` in the repo. It rebuilds that one automatically when the source is newer.

## Signing key

Create it once:

```sh
./spor keygen        # ~/.config/spor/spor.key (private) and spor.pub (public)
```

- **`spor.key`** stays on this machine. Back it up somewhere safe, like a password manager. If you lose it you need a new key and must re-sign every track.
- **`spor.pub`** goes to the server as `/etc/spor/spor.pub`. It isn't secret.

See [signing.md](signing.md) for why it works this way.

## SSH alias for the server

Give the VPS a short name in `~/.ssh/config`, so the IP lives in one place:

```sshconfig
Host spor-vps
    HostName <vps-ip>
    User <your-vps-user>
    # IdentityFile ~/.ssh/<key>   # only if you don't use your default key
```

Test it with `ssh spor-vps true`. Use the alias or the IP; the VPS's own hostname (what its prompt shows) won't resolve from your machine.

## Environment

Add these to `~/.zshrc` or `~/.bashrc`:

```sh
export SPOR_REMOTE=spor-vps:/srv/spor/library
export SPOR_RELOAD="sudo systemctl kill -s HUP spor.service"
```

| Variable | Used by | Default |
|---|---|---|
| `SPOR_KEY` | sign, publish, add-track | `~/.config/spor/spor.key` |
| `SPOR_PUB` | verify | `~/.config/spor/spor.pub` |
| `SPOR_REMOTE` | publish, `add-track.sh -p` | none (required to publish) |
| `SPOR_RELOAD` | publish (runs over ssh after upload) | `systemctl kill -s HUP spor.service` |
| `SPOR_URL` | printed share links only | `https://lytt.txtv.no` |
| `SPOR_LIBRARY` | add-track.sh | `./library` |
| `SPOR_BIN` | add-track.sh | see above |

`SPOR_RELOAD` uses `sudo` because the reload needs root. On the server, allow just that one command without a password (see [server.md](server.md#let-publish-upload-and-reload)). Without it, uploads still work and spor picks new tracks up within `rescan_interval` (60 seconds by default).

## Testing locally

Run the whole thing on your machine before touching the server. Use a throwaway key so test signatures never mix with real ones:

```sh
./spor keygen -out dev-keys                                   # dev-keys/ is gitignored
SPOR_KEY=dev-keys/spor.key SPOR_URL=http://localhost:8740 \
  scripts/add-track.sh ~/Music/test.mp3
./spor verify -pub dev-keys/spor.pub library/
./spor serve -library library -cache cache -pub dev-keys/spor.pub \
  -url http://localhost:8740 -listen 127.0.0.1:8740 -debug
```

Open http://localhost:8740.

- **`-url http://localhost:8740`** makes share links and preview tags point at your machine instead of `lytt.txtv.no`.
- **Reload after changes:** re-sign, then `pkill -HUP -x spor`, or wait for the 60-second rescan.
- **On your phone:** use `-listen 0.0.0.0:8740` and open `http://<your-lan-ip>:8740`. Playback works, but offline caching doesn't, because browsers only allow service workers over HTTPS or on localhost.
- **Link previews** (Signal, Discord, Messenger) can't be tested locally; their servers must reach the page from the internet.

Tracks signed with `dev-keys` won't verify on the real server. Before publishing for real, re-sign with your real key: `./spor sign -all library/`.

## Tests

```sh
go vet ./... && go test ./...
shellcheck scripts/add-track.sh deploy/install.sh
```

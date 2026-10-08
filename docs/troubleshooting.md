# Troubleshooting

## Publishing

**`spor: no track.<mp3|m4a|opus|ogg|webm|flac|mp4> found`**
Media files must be named `track.<ext>`. Rename (`mv song.mp4 track.mp4`) or use `scripts/add-track.sh`, which names them for you.

**`ssh: Could not resolve hostname …`**
`SPOR_REMOTE` points at a name your workstation can't resolve, often the VPS's own hostname from its prompt. Use the IP or an alias from `~/.ssh/config` (see [workstation.md](workstation.md#ssh-alias-for-the-server)). The track is already signed; retry with `./spor publish library/<slug>`.

**Publish seems stuck after the `rsync` line**
Usually it's just uploading; rsync shows no progress. Otherwise ssh is waiting for input: type `yes` for a new host key, or it wants a password because your key isn't used. On success it ends with the share link.

**`reload failed (…); the server picks the track up on its next poll`**
The passwordless sudo rule for the reload isn't set up (see [server.md](server.md#let-publish-upload-and-reload)). Harmless: the track appears within 60 seconds anyway.

**A track doesn't show up**
```sh
./spor verify library/<slug>               # on the workstation
journalctl -u spor -n 30 --no-pager        # on the VPS
```
Common causes: `visibility` is `unlisted` or `draft`; a file was changed after signing (re-sign and publish); it was signed with a different key than the server's `spor.pub` (e.g. `dev-keys`).

**Description shows "Other" or "Cover (front)"**
Fixed in `add-track.sh`: those were the embedded cover image's labels. Edit `meta.toml` and publish.

## Installing

**`install: cannot create regular file '/usr/local/bin/spor': Permission denied`**
The script ran as your normal user. Run `sudo GO="$(command -v go)" ./deploy/install.sh`.

**`go not found`** under sudo
`sudo` resets `PATH`. Pass Go explicitly: `sudo GO=/path/to/go ./deploy/install.sh`.

**Version shows `dev` instead of `v0.1.0-…`**
git refused to read the checkout as root ("dubious ownership"). Fixed in the current `install.sh`; `git pull` first.

**spor doesn't start**
`journalctl -u spor -n 30`. Typical: a typo or unknown key in `/etc/spor/spor.toml`, a non-https `suno_url`/`referral_url`, or a missing `/etc/spor/spor.pub`.

## The site

**The site doesn't load / Caddy config isn't in effect**
```sh
systemctl status caddy --no-pager | head -5
sudo caddy validate --config /etc/caddy/Caddyfile
grep -n 'import\|txtv' /etc/caddy/Caddyfile
journalctl -u caddy -n 30 --no-pager
dig +short lytt.txtv.no; curl -s -4 ifconfig.me; echo
sudo firewall-cmd --list-services
```
Usually one of these: the snippet was never imported, Caddy wasn't reloaded after editing, ports 80/443 are closed in `firewalld`, or DNS doesn't point at the VPS. Fixes are in [server.md](server.md#caddy).

**Old design or missing fonts after a deploy**
The browser's cached app is still running. Hard-refresh once (Ctrl+Shift+R).

**Link previews show the wrong text or picture**
Signal, Discord and others cache previews. Check that the page itself is right with `curl -s https://lytt.txtv.no/t/<slug> | grep og:`, then wait, or share the link with a harmless `?x=1` added.

**What is the green or grey arrow next to a title?**
The track is saved on this device for offline playback.

**The like button is greyed out**
This browser already liked the track. That's by design; see [stats-and-likes.md](stats-and-likes.md).

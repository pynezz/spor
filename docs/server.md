# Server setup (AlmaLinux VPS)

spor runs natively under systemd, built from a checkout of this repo, behind Caddy for HTTPS.

## Install or update

On the VPS:

```sh
git clone https://github.com/pynezz/spor ~/spor    # first time
cd ~/spor && git pull                               # updates
sudo GO="$(command -v go)" ./deploy/install.sh
```

- **Run it with `sudo`.** It writes to `/usr/local/bin`, `/etc/spor` and `/etc/systemd/system`. Without root it stops and prints this command.
- **Pass `GO=…`** because `sudo` resets `PATH`, so root usually can't find your Go install. If `command -v go` prints nothing, give the full path, e.g. `GO=/usr/local/go/bin/go`.

What it does:
1. Builds spor (version from `git describe`, e.g. `v0.1.0-2-gcb48e70`) and installs it to `/usr/local/bin/spor`.
2. Creates `/etc/spor` and `/srv/spor/library`.
3. Copies `deploy/spor.toml` to `/etc/spor/spor.toml` **only if that file doesn't exist yet**. New settings added to the repo later must be added to the live config by hand.
4. Installs `deploy/spor.service` and reloads systemd.
5. Restarts spor, or stops and asks for the public key if `/etc/spor/spor.pub` is missing.

## Public key

Generate the key on your **workstation** (see [signing.md](signing.md)), then:

```sh
scp ~/.config/spor/spor.pub spor-vps:/tmp/spor.pub          # on the workstation
sudo install -m 0644 /tmp/spor.pub /etc/spor/spor.pub && rm /tmp/spor.pub   # on the VPS
sudo systemctl enable --now spor
```

## Let `publish` upload and reload

Once, on the VPS:

```sh
sudo chown <your-user>: /srv/spor/library
echo '<your-user> ALL=(root) NOPASSWD: /usr/bin/systemctl kill -s HUP spor.service' \
  | sudo tee /etc/sudoers.d/spor-reload
sudo chmod 0440 /etc/sudoers.d/spor-reload && sudo visudo -cf /etc/sudoers.d/spor-reload
sudo dnf install rsync
```

## The service

`deploy/spor.service` runs spor as a throwaway system user (`DynamicUser`) with heavy sandboxing:

- **Library:** `/srv/spor/library`, read-only for spor.
- **State:** `/var/lib/spor` (`plays.log`, `likes.log`, `state.json`, cover thumbnails).
- **Public key:** handed in with `LoadCredential=`, never through the environment.
- **Limits:** 128 MB memory.

```sh
systemctl status spor --no-pager
journalctl -u spor -n 20 --no-pager      # look for "library indexed tracks=N"
curl -s localhost:8740/healthz            # ok
sudo systemctl kill -s HUP spor.service   # rescan now (also happens every 60 s)
```

## Caddy

`deploy/spor.snippet` is the Caddy config for the site. Caddy only reads `/etc/caddy/Caddyfile` and what it imports, so copy the snippet over and import it:

```sh
sudo cp ~/spor/deploy/spor.snippet /etc/caddy/spor.caddy
grep -q 'import /etc/caddy/spor.caddy' /etc/caddy/Caddyfile \
  || echo 'import /etc/caddy/spor.caddy' | sudo tee -a /etc/caddy/Caddyfile
sudo caddy validate --config /etc/caddy/Caddyfile && sudo systemctl reload caddy
```

What the snippet does:
- **`lytt.txtv.no`** is the only real site: one service-worker cache, one set of link-preview URLs.
- **`musikk.txtv.no` and `tracks.txtv.no`** redirect to it with a permanent `308`. To move the site to another name, swap which name has the site block and which ones redirect, and set `public_url` to match.
- **Text is compressed** (zstd/gzip). Media isn't, since it's already compressed.
- **`X-Forwarded-For`** is overwritten with the real client address, because spor uses it for rate limiting (`trust_proxy = true`).
- **Access log** to `/var/log/caddy/lytt.txtv.no.log`. See [logging.md](logging.md).

After editing anything, Caddy keeps running the old config until you `systemctl reload caddy`.

## Firewall, DNS, SELinux

```sh
sudo firewall-cmd --permanent --add-service=http --add-service=https && sudo firewall-cmd --reload
dig +short lytt.txtv.no      # must print the VPS's IP
curl -s -4 ifconfig.me       # the VPS's IP
```

- **Firewall:** AlmaLinux's `firewalld` blocks ports 80/443 by default. Caddy can't get certificates or serve the site until they're open.
- **DNS:** every name in the snippet must point at the VPS, or Caddy can't get certificates for it.
- **SELinux:** if it's set to enforcing, Caddy needs `sudo setsebool -P httpd_can_network_connect 1` to reach spor on port 8740.

## After a deploy

1. Add any new settings to `/etc/spor/spor.toml` (compare with `deploy/spor.toml`), then `sudo systemctl restart spor`.
2. Hard-refresh the site once (Ctrl+Shift+R) so the old cached app is replaced.
3. Check likes (like, reload, still liked), a share page, and a link preview in Signal or Discord.

No re-signing is needed for updates, and play counts from `plays.log` carry over.

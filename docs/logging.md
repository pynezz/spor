# Logs

## spor

spor doesn't log requests. Its journal only has startup and shutdown, scans ("library indexed tracks=N") and warnings:

```sh
journalctl -u spor -n 50 --no-pager
journalctl -u spor -f                  # follow
```

## Caddy access log

The `log` block in `deploy/spor.snippet` writes every request for `lytt.txtv.no` to `/var/log/caddy/lytt.txtv.no.log`, one JSON object per line. Caddy rotates it at 100 MB and keeps 10 files for up to 90 days.

**It contains full IP addresses**, which count as personal data under GDPR. Mention it in a privacy notice if you publish one. It also contains the `?k=` token of every unlisted link that's opened, since full URLs are logged.

Every request is logged, including covers, static files and each chunk of media when someone seeks, so it is noisy. Page views are the `/` and `/t/<slug>` lines.

### Watch it live, one line per request

Needs `jq` (`sudo dnf install jq`):

```sh
sudo tail -F /var/log/caddy/lytt.txtv.no.log \
  | jq -r --unbuffered '"\(.ts|floor|strflocaltime("%F %T"))  \(.request.client_ip)  \(.request.uri)"'
```

```
2026-10-08 21:14:03  84.210.5.17  /
2026-10-08 21:14:04  84.210.5.17  /api/library.json
2026-10-08 21:14:09  84.210.5.17  /t/albatrossalt
```

- **`tail -F`** (capital F) keeps following after Caddy rotates the file.
- **`--unbuffered`** makes `jq` print each line immediately when its output goes into another pipe; without it lines arrive in bursts.

### Filters

Hide your own IPs. This filters on the IP field itself, so it can't hide the wrong lines:

```sh
sudo tail -F /var/log/caddy/lytt.txtv.no.log | jq -r --unbuffered '
  select(.request.client_ip | test("^(194\\.127\\.|84\\.210\\.5\\.)") | not)
  | "\(.ts|floor|strflocaltime("%F %T"))  \(.request.client_ip)  \(.request.uri)"'
```

`\\.` is a literal dot, so `194.127.` matches only those digits at the start of the address.

Only page views (no media, covers, static files):

```sh
sudo tail -F /var/log/caddy/lytt.txtv.no.log | jq -r --unbuffered '
  select(.request.uri | test("^/(m|c|p|static)/") | not)
  | "\(.ts|floor|strflocaltime("%F %T"))  \(.request.client_ip)  \(.request.uri)"'
```

The whole file instead of only new lines: replace `sudo tail -F FILE |` with `sudo jq … FILE`.

**Don't filter with `grep -v "194.127.*.*"`.** In `grep`, `.` means any character. That pattern also matches IPs like `31.194.127.8` and random hex inside `/m/<sha256>` paths. If you use `grep`, use `grep --line-buffered -v -F "  194.127."`.

### Handy alias

In the VPS user's `~/.zshrc`:

```sh
alias sporlog='sudo tail -F /var/log/caddy/lytt.txtv.no.log | jq -r --unbuffered "\"\(.ts|floor|strflocaltime(\"%F %T\"))  \(.request.client_ip)  \(.request.uri)\""'
```

### Reading the paths

See [urls-and-api.md](urls-and-api.md). In short: `/m/` is someone listening, `/p/` is a track being opened in the player, `/c/` is covers, `POST /api/play/…` is a counted play.

# Signing and keys

## How it works

- **Your workstation** holds the **private key** (`~/.config/spor/spor.key`). `spor sign` hashes every file in a track folder and signs the list, producing `track.sig`.
- **The server** holds only the **public key** (`/etc/spor/spor.pub`). At each scan it checks every `track.sig` and every file hash. Tracks that don't check out are not served.
- **Browsers** check too: the share page fetches the manifest and public key, verifies the signature with WebCrypto, and shows "Signert og bekreftet".

## Why the private key stays on the workstation

- **You sign where you prepare tracks.** `add-track.sh`, `spor sign` and `spor publish` run on your machine, so the key has to be there anyway.
- **A hacked server can't add fake tracks.** Suppose someone gets into the VPS, or into the account `publish` uploads with, and drops files into the library. Without the private key they can't sign them, so spor refuses to serve them. If the private key lived on the server, the intruder could sign anything.
- **Rebuilding the server doesn't touch your key.** Reinstall, copy `spor.pub` back, and every existing signature still verifies.

The public key isn't secret. Copying it with `scp`, or pasting it, is fine.

## What is signed, and what isn't

Signed: the file names, the SHA-256 of each file, and the key id. Nothing else.

**No URL or domain is part of the signature.** The `-url` flag on `spor sign` and `spor publish` (or `SPOR_URL`) only sets the domain in the share link printed after signing. That's useful mainly for unlisted tracks, whose printed link includes the secret `?k=` token. Changing it changes nothing on disk.

So moving the site to another domain needs **no re-signing**: change `public_url` on the server (see [configuration.md](configuration.md)) and swap the redirect in the Caddy config so old links keep working (see [server.md](server.md#caddy)).

## Commands

```sh
./spor keygen [-out DIR] [-comment TEXT]   # new keypair (default ~/.config/spor)
./spor sign library/<slug>                 # sign one track
./spor sign -all library/                  # sign every new or changed track
./spor sign -force library/<slug>          # re-sign even if unchanged
./spor verify library/                     # check signatures with SPOR_PUB / -pub
```

`spor sign` also creates `peaks.bin` (and a cover frame for video) with ffmpeg when missing; `-ingest=false` turns that off.

## Changing keys

To switch to a new key:
1. `./spor keygen -out ~/.config/spor-new` (or move the old pair aside first).
2. Re-sign everything with the new private key: `SPOR_KEY=… ./spor sign -force -all library/`, then publish.
3. Put the new `spor.pub` on the server.

The server can trust several public keys during a switch-over: set `public_keys = "/etc/spor/old.pub,/etc/spor/new.pub"` in `spor.toml`.

Tracks signed with the throwaway `dev-keys/` key from local testing never verify on the real server. Re-sign them with the real key first.

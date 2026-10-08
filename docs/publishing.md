# Publishing tracks

All of this runs on your workstation, from the repo folder. It assumes the [workstation setup](workstation.md) is done (key, `SPOR_REMOTE`, `SPOR_RELOAD`).

## Add a track

```sh
scripts/add-track.sh -p ~/Music/song.mp3
```

That one command:
1. **Creates `library/<slug>/`** and copies the file in as `track.<ext>`.
2. **Fills in metadata** from the file's tags: title, genre (style), description (from the description or comment tag) and the original creation date. The publish date is today.
3. **Finds a cover:** `-c` if given, else an image next to the file (`cover`, `folder`, `front` or the same name as the track, as `.jpg`/`.png`), else the artwork embedded in the file.
4. **Writes `meta.toml`**, signs the folder, uploads it, reloads the server and prints the share link.

It warns when a track ends up with no genre or no description, but still creates it.

### Everything spelled out

```sh
scripts/add-track.sh -p \
  -t "Bomring (kompromiss)" \
  -s "norsk rap" \
  -D "Om å stå fast i bomringkøen og forhandle med seg selv." \
  -C 2025-11-03 \
  -c ~/Music/bomring-cover.jpg \
  -l ~/Music/bomring-tekst.txt \
  ~/Music/bomring.mp3
```

| Flag | Sets | Default |
|---|---|---|
| `-t` | title | title tag, else the file name |
| `-S` | slug: the folder name and the `/t/<slug>` part of the link | made from the title (`Nattlys på fjorden` → `nattlys-pa-fjorden`) |
| `-s` | style / genre | genre tag |
| `-D` | description | description or comment tag |
| `-C` | created: when it was originally made (`2024`, `2024-05` or `2024-05-12`) | date or creation_time tag |
| `-d` | publish date (`YYYY-MM-DD`) | today |
| `-v` | `public`, `unlisted` or `draft` | `public` |
| `-c` | cover image | see above |
| `-l` | lyrics text file | none |
| `-L` | library folder | `$SPOR_LIBRARY`, else `./library` |
| `-f` | replace an existing track with the same slug | refuse |
| `-p` | publish: sign, upload, reload | only sign |

### Special cases

```sh
# Unlisted: not in the library, only reachable with the printed ?k=… link
scripts/add-track.sh -p -v unlisted ~/Music/demo.mp3

# Several versions of one track: browsers pick the best they can play
scripts/add-track.sh -p ~/Music/song.opus ~/Music/song.mp3

# Prepare without publishing, check it, then publish
scripts/add-track.sh ~/Music/song.mp3
./spor publish library/<slug>
```

- **WAV and AIFF** are converted to opus (160 kbit/s) plus an mp3 fallback, because browsers handle those best.
- **Video (`.mp4`, `.m4v`)** becomes a video track with a "Video" badge. Without `-c`, the cover is a frame grabbed from the video at signing time.
- **Draft** tracks are signed and uploaded but not shown anywhere.

## Change a track

Edit the folder on your **workstation**, then publish again:

```sh
nano library/<slug>/meta.toml
./spor publish library/<slug>
```

`publish` re-signs, uploads only what changed, and reloads. The same goes for adding or replacing `cover.jpg` or `lyrics.txt`: change the file, then publish. See [track-format.md](track-format.md) for all `meta.toml` fields.

**Never edit files directly on the server.** The changed file would no longer match the signature, and spor hides the whole track until it's re-signed.

Alternatively, rebuild the folder from the original file with `scripts/add-track.sh -f -p …`. That overwrites `meta.toml`, so manual edits are lost.

## Rename (change the link)

The slug is the folder name, so renaming creates a new URL. Links already shared will stop working.

```sh
mv library/old-slug library/new-slug && ./spor publish library/new-slug
ssh spor-vps 'rm -r /srv/spor/library/old-slug && sudo systemctl kill -s HUP spor.service'
```

## Remove a track

```sh
ssh spor-vps 'rm -r /srv/spor/library/<slug> && sudo systemctl kill -s HUP spor.service'
```

To hide it without deleting, set `visibility = "draft"` and publish.

## Check before publishing

```sh
./spor verify library/              # every track: ok, or what's wrong
./spor sign -all library/           # sign everything new or changed
```

`spor sign` skips folders whose files haven't changed ("unchanged"); add `-force` to re-sign anyway.

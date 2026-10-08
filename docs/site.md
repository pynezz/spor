# The site: language, look and branding

## Language

The site is Norwegian (bokmål) for everyone, including the bots that build link previews in Signal, Discord and Messenger. `?lang=en` on any page switches to English, and every page has a small "In English" / "På norsk" link in the footer.

All interface text lives in two files:

- `web/strings/nb.json`: Norwegian
- `web/strings/en-US.json`: English

Edit a value, rebuild and deploy. Keys that need care:

| Key | Notes |
|---|---|
| `siteName` | page titles and link previews |
| `tagline` | next to the logo and in the library's preview text |
| `plays1` / `playsN`, `likes1` / `likesN`, `tracks1` / `tracksN` | singular and plural ("1 hjerte", "3 hjerter") |
| `sunoFollow` | `%s` is replaced by the handle from `suno_url` |
| `referralNote` | the small text under the referral button. The date in it is written by hand; update it when Suno's offer changes. |

Dates are formatted by the server (share page) and the browser (cards), both in Norwegian unless `?lang=en`.

## Look

Black, Tidal-style: white type, hairline dividers, big artwork, and a blurred copy of the cover as background light. One cyan accent (`--accent` in `web/static/app.css`) marks liked hearts and the playing track. Colours, spacing and sizes are CSS variables at the top of `app.css`.

Fonts are bundled in `web/static/fonts/` (Inter, Inter Tight; SIL Open Font License, license files next to them), because the site's security policy allows nothing from other servers.

## Logo and footer

| File | Used as |
|---|---|
| `web/static/spor-logo.png` | logo top-left on every page (`.logo img { height: 44px }` in `app.css`) |
| `web/static/brand.png` | "spor by skum" artwork in the footer |
| `assets/` | the full-size originals; kept outside `web/static` so they aren't built into the binary |

Everything in `web/static/` is embedded in the spor binary, so keep files there small. To replace an image, make a web-sized copy from the original, e.g.:

```sh
convert assets/brand.png -resize 1200x /tmp/b.png && pngquant --quality 70-90 --strip --output web/static/brand.png /tmp/b.png
```

## Footer buttons

| Button | Setting |
|---|---|
| "Følg @skumx på Suno" | `suno_url` |
| "250 gratis kreditter på Suno", plus the note under it | `referral_url` |

Both are set in `/etc/spor/spor.toml` (see [configuration.md](configuration.md)). An empty value hides the button. They open in a new tab and don't pass on which page the visitor came from.

## Offline playback

The first time someone plays a track, the browser saves it (up to `sw_cache_limit_mb`, least-recently-played removed first) and can replay it without a connection. A saved track gets a small download icon next to its title ("Lagret for avspilling uten nett"). This only works over HTTPS or on localhost.

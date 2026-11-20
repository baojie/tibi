# Clients

Anything that can send an HTTP request can upload to tibi. Every client
needs the server URL and an API token:

```sh
tibi admin token create --name laptop --scopes upload,delete,read_private
```

Give each client its own token (and only the scopes it needs) so you can
revoke one without touching the others.

## The tibi CLI

```sh
tibi login                     # server URL + token, saved to ~/.config/tibi/config.toml
tibi up photo.png notes.txt    # one URL per line, copied to the clipboard
cat log.txt | tibi             # stdin
tibi paste -l python           # $EDITOR, then a highlighted paste
tibi clip                      # whatever is on the clipboard (image first)
tibi up --album *.jpg          # one collection
tibi ls · tibi search WORDS · tibi rm ID · tibi share ID --ttl 1d
```

`tibi help` lists everything. Common flags: `-e 7d` expiry, `-p` private,
`--burn`, `--md` (print Markdown), `--raw` (print the direct URL).

## curl

```sh
TOKEN=tibi_…
# raw body (options as query parameters or X-Tibi-* headers)
curl -H "Authorization: Bearer $TOKEN" --data-binary @shot.png \
     "https://tibi.example.com/api/v1/items?filename=shot.png&expires=7d"
# multipart, several files into one collection
curl -H "Authorization: Bearer $TOKEN" -F file=@a.png -F file=@b.png \
     -F collection=new https://tibi.example.com/api/v1/items
```

The response is JSON: `url` (page), `raw_url` (direct link to embed),
`markdown`, `download_url`. The full API is in `docs/API.md`.

## Web page

Open `https://tibi.example.com/upload`, paste a token once, then drag files,
pick them, or press Ctrl+V to paste a screenshot or text. `/recent` lists,
searches and bulk-deletes your items.

## ShareX (Windows)

1. Edit `integrations/sharex/tibi.sxcu`: set `RequestURL` to your server and
   the `Authorization` header to `Bearer <token>`.
2. Double-click the file (or ShareX → Destinations → Custom uploader
   settings → Import → From file).
3. Destinations → Image uploader / Text uploader / File uploader → Custom
   … → tibi.

ShareX then copies the direct link (`{json:raw_url}`); change `URL` to
`{json:url}` for the page link instead.

## Flameshot (Linux)

`integrations/flameshot/tibi-shot` captures a region with Flameshot,
uploads it with the CLI (which must be logged in), copies the link and
shows a notification:

```sh
install -m 755 integrations/flameshot/tibi-shot ~/.local/bin/
# then bind "tibi-shot" to PrintScreen in your desktop's keyboard settings
tibi-shot --md        # extra flags go to `tibi up`
```

## PicGo / PicList

`integrations/picgo-plugin` is the `picgo-plugin-tibi` uploader. It returns
the direct image link, so Typora and other Markdown editors that use PicGo
insert `![](https://tibi.example.com/aZ3k9QxP.png)`.

```sh
picgo install /path/to/tibi/integrations/picgo-plugin
picgo set uploader tibi        # server URL, token, optional expiry
picgo use uploader tibi
```

In the PicGo or PicList app, install the plugin from that folder and fill
in the same settings.

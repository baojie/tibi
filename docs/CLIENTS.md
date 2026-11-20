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

`--encrypt` encrypts on your machine before uploading (`paste`, `up`,
`clip`): the server stores only ciphertext, and the printed link carries
the key after `#k=` — anyone with the whole link can read it, nobody
without it (including the server) can. Add `--password` to also require a
password (asked twice; `TIBI_E2E_PASSWORD` for scripts). The page decrypts
in the browser; the file name stays inside the encryption.

```sh
cat .env | tibi paste --encrypt -e 1d     # → https://tibi.example.com/aZ3k9QxP#k=…
tibi up --encrypt --password contract.pdf
```

Files over 20 MiB go up in resumable chunks with a progress line. If the
upload is interrupted (network, Ctrl-C, laptop lid), run the same command
again: it continues where it stopped instead of starting over.

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

For big files or flaky networks use the resumable endpoint
`/api/v1/uploads/` with any [tus](https://tus.io/implementations) client
(`tus-js-client`, `tus-android-client`, `tuspy`, …): send the token as
`Authorization: Bearer …` and options as `Upload-Metadata` (`filename`,
`title`, `expires`, …).

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

## AI agents (MCP)

`tibi mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server on stdio. It uses the same server and token as the CLI (`tibi login`,
or `TIBI_SERVER` / `TIBI_TOKEN`), so log in once first. Agents get five
tools:

| tool | does |
|---|---|
| `upload_file(path, title?, expires?, private?)` | upload a local file, return its URL (and Markdown for images) |
| `paste_text(text, lang?, title?, expires?, private?, encrypt?)` | publish text or code as a highlighted paste; `encrypt` makes it end-to-end encrypted |
| `list_items(limit?, kind?)` | recent uploads |
| `search_items(query)` | full-text search |
| `delete_item(id)` | delete by ID or URL |

Uploads are tagged `source=mcp`. Failures (bad token, file too large,
server down) come back as tool errors the agent can read and act on.

**Claude Code**

```sh
claude mcp add tibi -- tibi mcp            # this project
claude mcp add --scope user tibi -- tibi mcp   # all projects
```

Then ask, for example, "share this diff as a paste" or "upload
coverage.html and give me the link".

**Claude Desktop, Cursor, and other clients** take a JSON entry like:

```json
{
  "mcpServers": {
    "tibi": { "command": "tibi", "args": ["mcp"] }
  }
}
```

Add `"env": {"TIBI_SERVER": "https://tibi.example.com", "TIBI_TOKEN": "tibi_…"}`
if the client does not see your CLI login. Give agents a token with only the
scopes they need (`upload`, plus `delete` if they may delete).

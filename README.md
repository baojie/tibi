# tibi

**tibi** (题壁, *tí bì*, "writing on the wall") is named after the Tang and
Song custom of brushing poems onto the walls of inns, temples and post
stations, where any passerby could read them — 题诗于壁. It is a
self-hosted image host and pastebin: send an image, a screenshot, a code
snippet or any file from whatever program you are in, and get back a link
you can share.

One Go binary, one SQLite file, blobs on local disk or in S3-compatible
storage. No accounts to sign up for, no JavaScript framework, nothing
phoning home.

## What it does

- **Images**: PNG, JPEG, GIF, WebP, AVIF; metadata (GPS EXIF) stripped by
  default; thumbnails; iPhone HEIC converted for display. Direct-embed URLs
  (`/aZ3k9QxP.png`).
- **Pastes**: syntax highlighting with language detection, Markdown
  rendering, GBK and other legacy encodings converted, large logs
  truncated in the view with the full raw text one click away.
- **Any file**: video and audio play in the page (with seeking); everything
  else downloads with its original name.
- **Sharing controls**: expiry, private items, custom short links, burn
  after reading, signed time-limited share links, collections (albums),
  forks of pastes.
- **End-to-end encrypted pastes**: encrypted on your machine, decrypted in
  the recipient's browser; the key lives in the link's `#fragment`, which
  never reaches the server.
- **Search** across your pastes, Chinese included.
- **Cost control** for object storage: daily bandwidth caps, hotlink
  protection, per-token quotas; Prometheus metrics; export/import.
- **Resumable uploads** (tus) for multi-gigabyte files.
- **Safe by construction**: user content is never served as active content
  (HTML/SVG/JS come back as text or downloads), strict CSP, nothing indexed
  by search engines.

## Clients

From the command line (`tibi up photo.png`, `cat log | tibi paste`), curl,
the web upload page (paste a screenshot with Ctrl+V), a Chrome extension,
an Android app (share → link), ShareX, Flameshot, PicGo, the ACDSeeN image
viewer, and AI agents through MCP (`tibi mcp`). See
[docs/CLIENTS.md](docs/CLIENTS.md).

## Quick start (from source)

```sh
go build -o tibi ./cmd/tibi

export TIBI_DATA_DIR=./data TIBI_BASE_URL=http://127.0.0.1:8070
./tibi admin token create --name laptop --scopes upload --config ""   # prints the token once
./tibi serve --config "" &

./tibi login                      # server URL + token
./tibi up photo.png               # → http://127.0.0.1:8070/aZ3k9QxP
echo hello | ./tibi               # stdin works too
curl -H "Authorization: Bearer $TOKEN" --data-binary @file.bin \
     "http://127.0.0.1:8070/api/v1/items?filename=file.bin"
```

For a real deployment — release binaries, the `.deb` with its systemd
unit, the Docker image, S3 or Backblaze B2 — see
[docs/DEPLOY.md](docs/DEPLOY.md).

## URLs

| URL | meaning |
|---|---|
| `/{id}` | view page |
| `/{id}.{ext}` | raw bytes (direct embed) |
| `/{id}/raw` | raw bytes |
| `/{id}/dl` | download |
| `/c/{id}` | collection |
| `/s/{slug}` | custom short link |

## Documentation

| | |
|---|---|
| [docs/DEPLOY.md](docs/DEPLOY.md) | installing, configuration, storage, limits, monitoring, backups |
| [docs/CLIENTS.md](docs/CLIENTS.md) | CLI and every client integration |
| [docs/API.md](docs/API.md) | the HTTP API (generated from [api/openapi.yaml](api/openapi.yaml), also served at `/api/v1/openapi.yaml`) |
| [docs/SECURITY.md](docs/SECURITY.md) | security model, what end-to-end encryption does and does not protect, reporting vulnerabilities |
| [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) | what is tested, and how |
| [CHANGELOG.md](CHANGELOG.md) | release history |
| [PLAN.md](PLAN.md) | design and roadmap |

## Development

```sh
go test ./...                 # everything, local disk
scripts/test-s3.sh ./...      # again against an S3-compatible store (Docker)
scripts/e2e/run.sh            # browser tests: upload page, decryption, Chrome extension
android/scripts/emulator-test.sh   # Android app on an emulator
```

## License

tibi is free software under the [GNU Affero General Public License v3.0](LICENSE)
(AGPL-3.0-only): you may run, study, change and share it, and if you run a
modified version as a service for others, you must offer them its source.

Bundled third-party code: [highlight.js](https://highlightjs.org/) 11.11.1
(BSD-3-Clause, `web/static/vendor/highlight.LICENSE`), used to highlight
decrypted pastes in the browser.

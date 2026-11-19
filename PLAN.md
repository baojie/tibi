# tibi — Design and Roadmap

> **tibi** (题壁) — named after the Tang/Song practice of writing poems on public
> walls for any passerby to read. A self-hosted image host + pastebin: push any
> image, code snippet, or file from any program, get back a URL you can share.

This document is the brief for the implementing agent.

- **Part A** is the design: what the system is and the rules it must follow.
- **Part B** is the roadmap: versions `0.0.1` → `0.1.0`. Each version adds one or
  a few core features, ships with its acceptance tests passing, and is tagged.
  `0.1.0` is the complete feature set described in this document.

Build strictly version by version. Do not start a version until the previous
one is tagged. Within a version, keep commits small and tests green.

---

# Part A — Design

## A1. Goals and non-goals

### Goals
1. **One upload API** that any program can call (curl, scripts, CLI, browser
   extension, Android, adseen, AI agents) to upload an image, a text/code
   snippet, or any file.
2. **Pastebin core**: text and code get a shareable page with syntax
   highlighting, raw view, and download.
3. **Image hosting core**: images get a stable direct URL with a file extension,
   suitable for embedding in Markdown/HTML.
4. **Self-hosted, single binary, low ops**: one Go binary, SQLite, blobs on local
   disk or in S3-compatible object storage (AWS S3, Backblaze B2, R2, MinIO).
5. **Open source, international**: English code, docs, and UI strings; UI strings
   kept in one catalog so translations can be added.

### Non-goals (never build)
- No sync of any kind: no folders, no desktop sync client, no "drive" features.
- No public sign-up or multi-tenant accounts. Single-owner software; access is
  via API tokens the owner creates.
- No comments, likes, social features, or public gallery/explore page.
- **Content is immutable.** An item's bytes never change after upload. "Editing"
  a paste means **forking** it into a new item (with `forked_from` set). This
  matches content-addressed storage and means a shared link always shows what
  was shared.

## A2. Architecture

```
   tibi CLI · curl · ShareX · PicGo · Flameshot · Chrome ext · Android · adseen · MCP agents
                                   │ HTTPS, Bearer token
                           ┌───────▼────────┐
                           │   tibi serve   │  Go, net/http
                           │  /api/v1/...   │  JSON API (+ tus resumable uploads)
                           │  /{id} ...     │  public view pages + raw
                           └───┬────────┬───┘
                               │        │
                  SQLite (metadata,     BlobStore: fs | s3 (S3 / B2 / R2 / MinIO)
                   FTS5 search index)
```

- **Language**: Go (≥1.22). One binary `tibi`: `tibi serve` runs the server,
  `tibi admin …` manages it, `tibi mcp` runs the MCP server, everything else is
  the client CLI.
- **DB**: SQLite via `modernc.org/sqlite` (pure Go, no cgo). WAL mode.
  Forward-only migrations embedded in the binary, applied on startup. The DB is
  always local, even when blobs are in S3.
- **Templates/static**: `html/template` + `embed`, no frontend build step. Small
  vanilla JS only where needed (copy buttons, upload page, E2E decryption).
- **Highlighting**: `github.com/alecthomas/chroma/v2`, server-side.
- **Markdown**: `github.com/yuin/goldmark`, raw HTML disabled.
- **API contract**: `api/openapi.yaml` is the source of truth for `/api/v1`.
  Served at `/api/v1/openapi.yaml`. A contract test checks every handler
  against it. Client code for Chrome/Android/adseen is generated from it.

### Repo layout
```
cmd/tibi/            main.go — subcommand dispatch
api/openapi.yaml     API contract
internal/server/     HTTP handlers, middleware (auth, limits, logging, metrics)
internal/store/      SQLite access, migrations, FTS
internal/blob/       BlobStore interface + fs and s3 backends
internal/ingest/     upload pipeline: sniffing, charset, metadata strip, HEIC
internal/render/     highlighting, markdown, view models
internal/client/     Go API client (used by CLI and MCP)
internal/cli/        CLI commands, config, clipboard helpers
internal/mcp/        MCP server
web/templates/       view pages
web/static/          CSS, small JS
web/i18n/            UI string catalogs (en.toml first)
integrations/        sharex/, flameshot/, picgo-plugin/
extensions/chrome/   Chrome extension
android/             Android app
docs/                API.md (generated from OpenAPI), DEPLOY.md, CLIENTS.md
packaging/           Dockerfile, nfpm (deb), Homebrew formula
```

## A3. URLs and IDs — frozen from 0.0.1

Shared links must never break, so this scheme is fixed in the first release and
may only be extended, never changed:

| URL | meaning |
|---|---|
| `/{id}` | item view page |
| `/{id}.{ext}` | raw bytes, direct-embed URL (images) |
| `/{id}/raw` | raw bytes (text served as `text/plain; charset=utf-8`) |
| `/{id}/dl` | download (`Content-Disposition: attachment`) |
| `/s/{slug}` | custom slug → 302 to `/{id}` |
| `/c/{id}` | collection (album / multi-file paste) view page |
| `/api/v1/...` | API |

- **Item IDs**: 8 chars base62 from `crypto/rand` (~47 bits); retry on collision.
  Collection IDs use the same generator and the same namespace (no ID is ever
  both an item and a collection).
- **Slugs**: optional, owner-chosen, `[a-z0-9-]{3,64}`, unique, reserved words
  blocked (`api`, `s`, `c`, `static`, `admin`, `robots.txt`, …). A slug can be
  reassigned to another item (it is a pointer); item IDs never change.
- Reserved top-level paths must not collide with 8-char base62 IDs (they are
  all either shorter, contain `.`/`/`, or are on a blocklist checked at ID
  generation).

## A4. Data model

### `items`
| column | type | notes |
|---|---|---|
| id | TEXT PK | see A3 |
| kind | TEXT | `image` \| `video` \| `audio` \| `text` \| `file` \| `encrypted` |
| mime | TEXT | detected type of stored bytes |
| orig_mime | TEXT NULL | if converted (e.g. HEIC→JPEG), the original type |
| filename | TEXT NULL | original name |
| ext | TEXT | canonical extension for `/{id}.{ext}` |
| size | INTEGER | stored bytes |
| sha256 | TEXT | blob key, hash of **stored** bytes |
| title | TEXT NULL | |
| lang | TEXT NULL | chroma lexer for text |
| charset | TEXT NULL | original charset if transcoded to UTF-8 (e.g. `GBK`) |
| width, height | INTEGER NULL | images/video |
| duration_ms | INTEGER NULL | video/audio, if cheaply known |
| visibility | TEXT | `unlisted` (default) \| `private` |
| slug | TEXT NULL UNIQUE | see A3 |
| collection_id | TEXT NULL | FK → collections |
| position | INTEGER NULL | order within collection |
| forked_from | TEXT NULL | item id |
| burn_after_read | INTEGER | 0/1 |
| source | TEXT NULL | client tag: `cli`, `chrome`, `android`, `adseen`, `mcp`, `sharex`, … |
| meta | TEXT NULL | JSON for client-specific metadata (e.g. `{"adseen_id": "…", "page_url": "…"}`) |
| token_id | INTEGER | uploader |
| view_count | INTEGER | view page + raw hits |
| bytes_served | INTEGER | for bandwidth caps |
| created_at | INTEGER | unix seconds |
| expires_at | INTEGER NULL | null = never |
| deleted_at | INTEGER NULL | soft delete |

Indexes: `created_at`, `sha256`, `expires_at`, `collection_id`, `source`.

### `collections`
`id`, `title`, `visibility`, `slug`, `token_id`, `created_at`, `expires_at`,
`deleted_at`. Deleting a collection soft-deletes its items.

### `tokens`
`id`, `name`, `hash` (SHA-256 of token), `scopes` (`upload`, `read_private`,
`delete`, `admin`), `quota_bytes` (NULL = unlimited), `created_at`,
`last_used_at`, `revoked_at`. Token format `tibi_` + 32 base62 chars, shown once.

### `items_fts` (FTS5)
Indexes `title`, `filename`, and text content (first `max_index_bytes`, default
1 MiB) of `text` items. Tokenizer: `unicode61` plus a trigram index
(`tokenize='trigram'`) so CJK substring search works. Encrypted items are never
indexed.

### `uploads` (resumable sessions, see A6)
Managed by the tus handler's store; plus a row linking upload ID → token and
pending item options.

## A5. Ingest pipeline

Every upload, regardless of transport, goes through the same steps:

1. Stream to a temp file on local disk, enforcing `max_upload_bytes` and token
   quota as bytes arrive.
2. **Sniff** first 512 bytes + filename extension.
3. **Classify**:
   - png/jpeg/gif/webp/avif → `image`
   - heic/heif → `image`; convert to JPEG if `convert_heic = true` and a
     converter is available (see below), keeping `orig_mime`. If no converter,
     store as-is; the view page shows a download button and a note.
   - mp4/webm/mov → `video`; mp3/ogg/opus/m4a/wav/flac → `audio`
   - SVG, HTML, XHTML, JS → always `file` (never rendered; see A8)
   - text: valid UTF-8 without NUL → `text`. Otherwise run charset detection
     (`golang.org/x/net/html/charset` / `chardet`); if confidently GBK, GB18030,
     Big5, Shift_JIS, EUC-KR, Latin-1, UTF-16 with BOM → transcode to UTF-8,
     record `charset`, kind `text`. Raw download serves the **transcoded**
     UTF-8; the original bytes are not kept (documented behavior).
   - text over `max_text_bytes` → stored as `text` but view page shows the
     first N lines with a "view raw" link.
   - anything else → `file`
   - client may force `kind=text|file`; never `image`/`video` on non-media.
4. **Strip image metadata** (EXIF incl. GPS, XMP) losslessly for JPEG/PNG/WebP
   unless `keep_meta=1`. Read dimensions.
5. Hash stored bytes → `Exists`? skip : `Put` → insert DB row (and FTS row).
   DB row only after `Put` succeeds; crashes may leave orphan blobs, never
   dangling rows.

**HEIC conversion**: no mature pure-Go decoder exists. At startup, detect an
external converter in this order: `vips`, `heif-convert`, `magick`. Config
`heic_converter = "auto" | "vips" | "heif-convert" | "magick" | "off"`. The
Docker image includes `libvips-tools`. Run with a timeout and size limit.

## A6. HTTP API (v1)

JSON everywhere. Errors: `{"error":{"code":"…","message":"…"}}`.
Auth: `Authorization: Bearer tibi_…`.

### Upload — `POST /api/v1/items`
**A. multipart/form-data**: one or more `file` parts. Optional fields: `title`,
`lang`, `expires`, `visibility`, `kind`, `slug`, `source`, `meta`,
`keep_meta`, `burn_after_read`, `collection` (`new` to create one from all
parts, or an existing collection id to append), `collection_title`.

**B. raw body**: body = content; options as query params or `X-Tibi-*` headers
(`filename`, `title`, …).

`expires`: `10m`, `1h`, `7d`, `30d`, `never`.

Response `201` (array if multiple files; plus `collection` object if created):
```json
{
  "id": "aZ3k9QxP", "kind": "image",
  "url": "https://tibi.example.com/aZ3k9QxP",
  "raw_url": "https://tibi.example.com/aZ3k9QxP.png",
  "download_url": "https://tibi.example.com/aZ3k9QxP/dl",
  "markdown": "![](https://tibi.example.com/aZ3k9QxP.png)",
  "mime": "image/png", "size": 48213, "width": 1280, "height": 720,
  "visibility": "unlisted", "expires_at": null, "created_at": 1763575200
}
```

### Resumable upload — tus 1.0 at `/api/v1/uploads/`
Use `github.com/tus/tusd/v2` handler. Upload metadata carries the same options
as above. On completion the file enters the ingest pipeline and the item JSON is
retrievable at `GET /api/v1/uploads/{upload_id}/item`. Incomplete uploads expire
after 24h. Recommended for anything > 20 MiB and for all mobile uploads.
Clients: CLI (built-in), Android (`tus-android-client`), Chrome (`tus-js-client`).

### Other endpoints
| method | path | scope | purpose |
|---|---|---|---|
| GET | `/api/v1/items?limit&cursor&kind&source&collection` | upload | list, newest first |
| GET | `/api/v1/items/{id}` | upload | metadata |
| PATCH | `/api/v1/items/{id}` | upload | `title`, `lang`, `visibility`, `expires`, `slug` (never content) |
| DELETE | `/api/v1/items/{id}` | delete | soft delete |
| POST | `/api/v1/items/{id}/fork` | upload | body = new content; creates new item with `forked_from` |
| POST | `/api/v1/items/{id}/share-link` | upload | signed time-limited URL for a private item (`?ttl=1h`) |
| GET | `/api/v1/search?q&kind&limit&cursor` | upload | FTS search |
| POST/GET/PATCH/DELETE | `/api/v1/collections[/{id}]` | upload/delete | manage collections; reorder items |
| GET | `/api/v1/stats` | admin | storage used, item counts, bytes served, top items |
| GET | `/api/v1/whoami` | any | token name, scopes, quota |
| GET | `/api/v1/openapi.yaml` | none | contract |
| GET | `/healthz`, `/metrics` | none / admin-or-localhost | liveness, Prometheus |

### Public routes
- `/{id}` view page by kind: image (with copy-link/Markdown buttons), video/audio
  (`<video>`/`<audio>` player), text (highlighted, line numbers, `#L12` and
  `#L12-L20` anchors, wrap toggle, copy, raw, "fork" button for the owner),
  markdown (rendered/source toggle), file (name, size, download), encrypted
  (decrypt in browser, A9).
- `/c/{id}` collection: image grid with thumbnails, or a stacked multi-file
  paste view.
- All raw routes support `Range`.
- `private`: 404 unless token with `read_private` or a valid signed share link.
- Expired / deleted / burned: 404 (don't leak existence).
- `burn_after_read`: deleted after the first successful **view page** render by
  a non-owner (not on raw prefetch by chat-app link previewers — the view page
  shows a "click to reveal" interstitial, and the reveal is what burns).
- OpenGraph/Twitter card tags on view pages (title, image thumbnail for images)
  so links preview in chat apps — but **not** for private, encrypted, or
  burn-after-read items.

## A7. Blob storage

```go
type BlobStore interface {
    Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error
    Open(ctx context.Context, key string) (io.ReadSeekCloser, error)
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    List(ctx context.Context, prefix string, fn func(key string) error) error // for orphan GC
    PresignGet(ctx context.Context, key string, ttl time.Duration, filename, mime string) (url string, ok bool, err error)
}
```
Keys: `ab/cd/<sha256>`; thumbnails: `thumb/ab/cd/<sha256>-<w>.webp`.

1. **`fs`** (default): `data_dir/blobs/…`, temp file + atomic rename.
2. **`s3`**: any S3-compatible store — AWS S3, **Backblaze B2** (S3-compatible
   API), Cloudflare R2, MinIO, Wasabi — via `github.com/minio/minio-go/v7`.
   One implementation; no B2-native backend. Objects private, with
   `Content-Type` and `Cache-Control: public, max-age=31536000, immutable`.

**Delivery** (`blob_delivery`):
- `proxy` (default): tibi streams the blob; all A8 headers apply.
- `redirect`: 302 to a presigned URL (TTL 1h). Only for `image`/`video`/`audio`
  and `/dl`; text and unsafe types are always proxied.
- Optional local LRU read cache for `s3` (`cache_dir`, `cache_max_bytes`).

**GC**: every 10 min — hard-delete items expired or soft-deleted > 24h ago,
then delete unreferenced blobs and thumbnails. `tibi admin gc --orphans` scans
the store with `List` and removes keys with no DB reference.

**Migration**: `tibi admin blobs migrate --to s3|fs` copies all referenced blobs,
verifies hashes, is idempotent and resumable; then switch config.

## A8. Security and privacy

1. Never serve user content as active content. Raw routes for HTML, SVG, XHTML,
   JS, XML → `text/plain` (raw) or attachment (dl). Always
   `X-Content-Type-Options: nosniff`.
2. Raw routes: `Content-Security-Policy: default-src 'none'; sandbox`,
   `Cross-Origin-Resource-Policy: cross-origin`.
3. View pages: strict CSP (`default-src 'self'`), no inline scripts.
4. `raw_base_url` config to serve raw content from a separate origin.
5. **No indexing**: every view page and raw response sends
   `X-Robots-Tag: noindex, nofollow, noarchive`; `/robots.txt` disallows all;
   view pages also carry `<meta name="robots" content="noindex">`.
6. Image metadata stripped by default (A5).
7. Limits: `max_upload_bytes` (default 50 MiB for single-request upload,
   `max_resumable_bytes` default 2 GiB), `max_text_bytes`, per-token
   `quota_bytes`, per-token rate limit (token bucket).
8. Tokens stored hashed, constant-time compare, never logged.
9. Signed share links: HMAC-SHA256 over `id|expiry` with a server secret
   (`secret_key_file`, generated on first start).
10. **Bandwidth protection** (cost control for S3/B2 egress):
    - per-item and global `bytes_served` counters (batched writes, not per request)
    - `max_item_bytes_per_day` and `max_total_bytes_per_day`; when exceeded, raw
      routes return 429 for that item / globally (view page explains)
    - optional `allowed_referers` list for raw image routes (hotlink protection);
      empty referer always allowed (direct visits, chat apps)
11. Secrets (S3 keys) from env or a file readable only by the service user;
    refuse to start if a config file holding secrets is world-readable.

## A9. End-to-end encrypted pastes

PrivateBin-style; the server never sees plaintext or key.
- Client generates a random 256-bit key, encrypts with AES-256-GCM, uploads
  ciphertext with `kind=encrypted` (+ optional `lang` hint, which is not secret).
- Share URL: `https://host/{id}#k=<base64url key>`. The fragment is never sent
  to the server.
- View page loads ciphertext from `/{id}/raw` and decrypts with WebCrypto in a
  static JS file, then highlights client-side (bundle a small highlighter, e.g.
  highlight.js core + common languages, served from `/static`).
- Optional password: key = HKDF(random key ‖ PBKDF2(password)).
- CLI: `tibi paste --encrypt`, `tibi up --encrypt FILE` (any file; view page
  offers decrypted download).
- Encrypted items: no FTS, no OpenGraph, no metadata stripping (opaque bytes).
- Format versioned: first byte of ciphertext blob = format version.

## A10. CLI

stdout prints **only** result URLs (one per line); everything else goes to
stderr. `--json` prints full API responses.

Config `~/.config/tibi/config.toml` (XDG), env `TIBI_SERVER`, `TIBI_TOKEN`:
```toml
server = "https://tibi.example.com"
token  = "tibi_..."
copy   = true            # copy URL to clipboard when stdout is a TTY
default_expires = "never"
```

```
tibi login                        # prompt server + token, verify via /whoami
tibi up FILE...                   # upload; >20 MiB uses tus automatically
tibi up -  |  <cmd> | tibi        # stdin
tibi up --album a.png b.png       # one collection, prints collection URL
tibi paste [-l LANG] [-t TITLE]   # stdin, or $EDITOR if stdin is a TTY
tibi clip                         # upload clipboard (image preferred, else text)
tibi fork ID [FILE|-]             # new version of a paste; $EDITOR prefilled if no input
tibi ls [-n 20] [--kind K] [--source S]
tibi search QUERY
tibi info ID | tibi rm ID... | tibi open ID
tibi slug ID NAME                 # set custom slug
tibi share ID [--ttl 1h]          # signed link for a private item
```
Common flags: `-e/--expire`, `-p/--private`, `-t/--title`, `-l/--lang`,
`-n/--name`, `--slug`, `--burn`, `--encrypt`, `--keep-meta`, `--md`, `--raw`,
`--json`, `--no-copy`, `--open`.

Clipboard: Wayland `wl-paste`/`wl-copy`, X11 `xclip`/`xsel`, macOS
`pbpaste`/`pbcopy`. For `clip`, list available types first and prefer
`image/png`. Missing tool → error naming the package.

Lang inference: `--lang` > extension > chroma analysis > `text`.

## A11. Admin and operations

```
tibi serve [--config PATH]
tibi admin token create --name N --scopes upload,delete,read_private [--quota 10GiB]
tibi admin token list | revoke N
tibi admin gc [--orphans]
tibi admin blobs migrate --to s3|fs
tibi admin export --out tibi-export.tar.zst   # all items + metadata JSON, decrypted-at-rest form
tibi admin import tibi-export.tar.zst         # restore into an empty instance
tibi admin stats
```

Server config `/etc/tibi/server.toml`, env overrides `TIBI_*`:
```toml
listen = "127.0.0.1:8070"
base_url = "https://tibi.example.com"
raw_base_url = ""
data_dir = "/var/lib/tibi"
default_expires = "never"
max_upload_bytes = 52428800
max_resumable_bytes = 2147483648
max_text_bytes = 2097152
strip_image_meta = true
convert_heic = true
heic_converter = "auto"
blob_delivery = "proxy"            # proxy | redirect
cors_origins = []                  # e.g. ["chrome-extension://<id>"]
allowed_referers = []
max_item_bytes_per_day = 0         # 0 = unlimited
max_total_bytes_per_day = 0
default_locale = "en"

[storage]
backend = "fs"                     # fs | s3

[storage.s3]
endpoint = "s3.us-west-004.backblazeb2.com"   # B2 example; AWS: s3.amazonaws.com
region = "us-west-004"
bucket = "tibi-blobs"
prefix = "blobs/"
access_key = ""                    # prefer env TIBI_S3_ACCESS_KEY
secret_key = ""                    # prefer env TIBI_S3_SECRET_KEY
use_path_style = false             # true for MinIO
```
- On startup with `s3`: probe Put/Exists/Delete and fail fast with a clear message.
- Structured logs via `log/slog`. Prometheus metrics at `/metrics`
  (requests, upload bytes, served bytes, GC stats, storage used).
- Backups: DB via Litestream (documented, not built) + `admin export`.
- TLS at the reverse proxy; `docs/DEPLOY.md` has systemd, Caddy, nginx
  (`client_max_body_size`), Cloudflare-in-front-of-B2 notes.

## A12. Clients and integrations

- **ShareX** (Windows): `integrations/sharex/tibi.sxcu` custom uploader config
  (multipart to `/api/v1/items`, URL from `$json:url$`).
- **Flameshot** (Linux): `integrations/flameshot/tibi-shot` script:
  `flameshot gui --raw | tibi up -n screenshot.png -`.
- **PicGo / PicList**: `integrations/picgo-plugin/` npm package
  `picgo-plugin-tibi` (uploader with server + token settings).
- **MCP**: `tibi mcp` runs a stdio MCP server using the CLI config. Tools:
  `upload_file(path, title?, expires?, private?)`,
  `paste_text(text, lang?, title?, expires?, private?, encrypt?)`,
  `list_items(limit?, kind?)`, `search_items(query)`, `delete_item(id)`.
  Returns URLs. Lets Claude Code and other agents share artifacts in one step.
- **Chrome extension** (MV3): options page (server, token in
  `chrome.storage.local`); context menus: image → upload; selection → paste;
  page → visible-area screenshot; popup with recent items. Copies URL +
  notification. `source=chrome`, `meta.page_url`. Uses tus for large files.
- **Android**: Kotlin share target (`ACTION_SEND`, `ACTION_SEND_MULTIPLE` for
  `image/*`, `video/*`, `text/plain`, `*/*`). Upload via WorkManager + tus,
  copy URL, notification with Copy/Share. Multiple images → collection.
  Quick Settings tile: upload latest screenshot. `source=android`.
- **adseen**: ordinary API client with its own token; `source=adseen`,
  `meta.adseen_id`; stores returned `id`/`url`; batch export → collection.
  Concrete integration designed after reviewing adseen's code.

## A13. Project matters

- **License**: **DECISION PENDING (owner)**. Options: AGPL-3.0 (server
  modifications offered as a service must be shared — recommended for a
  self-hosted server), or Apache-2.0/MIT (maximally permissive). The agent must
  not choose; leave `LICENSE` absent and the repo private until the owner decides.
- **Versioning**: SemVer, pre-1.0 per Part B. `CHANGELOG.md` updated each
  version. Git tag `vX.Y.Z` per version. DB migrations forward-only from 0.0.1.
  URL scheme (A3) frozen from 0.0.1. API may change before 0.1.0 but every
  change must update `openapi.yaml` and the CHANGELOG.
- **Packaging**: GitHub Actions release on tag → binaries (linux amd64/arm64,
  darwin arm64, windows amd64 client), Docker image (multi-arch, includes
  `libvips-tools`), `.deb` via nfpm, Homebrew tap formula.
- **i18n**: all UI strings in `web/i18n/en.toml`; locale from
  `Accept-Language`, fallback `default_locale`. Ship `en` and `zh-CN`.

---

# Part B — Roadmap

Each version lists **scope** and **done when** (acceptance tests; automated
unless marked manual). API tests use `httptest.Server`; CLI tests run the CLI
against it; S3 tests run against MinIO (Docker service in CI, skipped locally if
`TIBI_TEST_S3_ENDPOINT` is unset).

### 0.0.1 — Walking skeleton: upload any file, get a link
Scope: repo scaffold; config loading; `tibi serve`, `/healthz`; SQLite +
migrations; `fs` blob store; token admin (`create/list/revoke`); raw-body
`POST /api/v1/items` storing any file as kind `file`; frozen URL scheme (A3)
with `/{id}`, `/{id}/raw`, `/{id}/dl`, `/{id}.{ext}`; security headers A8.1–3
and noindex A8.5; CLI `login`, `up FILE`, `up -`, bare `tibi` with piped stdin;
`api/openapi.yaml` started; CI (build + test); CHANGELOG.
Done when: `curl --data-binary @x.bin` and `tibi up x.bin` both return a URL
whose `/dl` returns identical bytes; 401 without/with revoked token; 413 over
limit; `/robots.txt` disallows all and `X-Robots-Tag` is present on every
public route; uploading `evil.html` never yields `text/html` from any route.

### 0.0.2 — Image hosting core
Scope: sniffing and kind classification (A5 steps 2–3, except HEIC/media/
charset); multipart upload (multiple files → array); EXIF/GPS stripping +
dimensions; image view page; `markdown` field; CLI `--md`, `--raw`, clipboard
copy of result, `tibi clip`.
Done when: PNG round-trips via `raw_url` ending `.png` with `image/png`; JPEG
with GPS EXIF has none on download; `keep_meta=1` preserves it; same file twice
→ two IDs, one blob; SVG upload is kind `file` and never served as
`image/svg+xml`; `tibi clip` uploads a copied screenshot (manual).

### 0.0.3 — Pastebin core
Scope: text view page (chroma, line numbers, `#L12`/`#L12-L20`, wrap toggle,
copy); markdown rendered/source toggle; lang inference; charset detection and
transcoding (GBK etc.); large-text truncation; `tibi paste` (stdin / $EDITOR).
Done when: `echo 'print(1)' | tibi paste -l python` prints one URL with
highlighted Python; `cat main.go | tibi` infers Go; a GBK-encoded file shows
correct Chinese and records `charset=GBK`; a 5 MiB log shows truncated view +
working raw link.

### 0.0.4 — Item management and lifecycle
Scope: list/info/patch/delete endpoints with cursor pagination; `expires` +
GC loop (items and blobs); `private` visibility; custom slugs `/s/{slug}`;
burn-after-read with reveal interstitial; CLI `ls`, `info`, `rm`, `open`,
`slug`, flags `-e`, `-p`, `--slug`, `--burn`.
Done when: `--expire 1s` item 404s after expiry and its blob is gone after GC
(when unreferenced); deleting one of two identical uploads keeps the blob;
private item 404 without token, 200 with `read_private`; slug redirects and
reserved slugs are rejected; burn item survives a raw prefetch and a link-
preview fetch of the view page, and 404s after one reveal.

### 0.0.5 — Object storage: S3 / Backblaze B2
Scope: `s3` backend (A7); startup probe; `proxy` and `redirect` delivery;
`List` + `gc --orphans`; `admin blobs migrate`; `Range` support on all raw
routes; secrets-file permission check; MinIO in CI.
Done when: the entire test suite passes on both `fs` and `s3`; `redirect` mode
302s images to a working presigned URL while text stays proxied as
`text/plain`; migrating a populated `fs` instance to `s3` keeps every raw URL
byte-identical; `Range: bytes=0-99` returns 206 with 100 bytes; manual check
against a real B2 bucket (documented in DEPLOY.md).

### 0.0.6 — Media: video, audio, HEIC
Scope: `video`/`audio` kinds with inline players; HEIC→JPEG via detected
converter (A5); thumbnails for images (WebP, stored as blobs, generated on
upload, used later by collections and OpenGraph).
Done when: an mp4 plays in the view page and seeks (uses Range); an iPhone/
Android HEIC photo displays as JPEG with `orig_mime=image/heic`; with converter
`off`, HEIC is stored as-is and offered for download; thumbnail exists for
each image and is GC'd with it.

### 0.0.7 — Search and API contract
Scope: FTS5 index (unicode61 + trigram) over title, filename, text content;
`/api/v1/search`; `tibi search`; finalize `openapi.yaml` for all endpoints so
far, serve it, contract test, generate `docs/API.md`.
Done when: searching an English word, a code identifier, and a 2-character
Chinese substring each find the right paste; deleted/expired items never
appear; contract test fails if a handler's response diverges from the spec.

### 0.0.8 — Collections and forks
Scope: `collections` table and API; multipart `collection=new`; `/c/{id}`
(image grid with thumbnails / stacked multi-file paste); reorder; CLI
`up --album`; `POST /items/{id}/fork`, fork button on owner's view, `tibi fork`.
Done when: `tibi up --album a.png b.png c.txt` prints one collection URL showing
all three in order; deleting the collection soft-deletes its items; forking a
paste creates a new ID with `forked_from` while the original is unchanged.

### 0.0.9 — Web experience
Scope: owner web upload page (drag-drop, paste-from-clipboard, token in
browser storage); owner recent-items page with bulk delete; OpenGraph/Twitter
cards (with A6 exclusions); signed share links for private items + `tibi share`;
i18n catalog with `en` and `zh-CN`.
Done when: pasting an image into the upload page yields a link; Slack/Discord-
style unfurl fetch gets OG tags for an unlisted image but not for private/
burn/encrypted items; signed link works until expiry, then 404s; UI renders in
Chinese with `Accept-Language: zh-CN`.

### 0.0.10 — Operations: cost control, metrics, export
Scope: `view_count`, `bytes_served` (batched); per-item and global daily byte
caps → 429; `allowed_referers`; per-token `quota_bytes`; `/metrics`;
`/api/v1/stats` + `tibi admin stats`; `admin export` / `admin import`;
Litestream section in DEPLOY.md.
Done when: an item over its daily cap returns 429 on raw but its view page
still explains why; hotlinking from a non-allowed referer is blocked while
empty referer works; upload beyond quota → 413 with a clear message; export
from instance A and import into empty instance B reproduces every URL
byte-identically (fs and s3).

### 0.0.11 — Packaging and ecosystem compatibility
Scope: release workflow (binaries, multi-arch Docker with libvips, `.deb`,
Homebrew tap); `integrations/sharex/tibi.sxcu`; Flameshot script;
`picgo-plugin-tibi`; `docs/CLIENTS.md` covering curl, ShareX, Flameshot,
PicGo/PicList.
Done when: `docker run` with a mounted data dir serves uploads and converts
HEIC; `.deb` installs a working systemd unit; ShareX config and PicGo plugin
each upload successfully (manual); tagging a version publishes all artifacts.

### 0.0.12 — MCP server
Scope: `tibi mcp` (stdio) with tools listed in A12; `source=mcp`; docs for
adding it to Claude Code and other MCP clients.
Done when: an MCP client test harness calls `paste_text` and `upload_file`
and receives working URLs; errors (bad token, too large) come back as tool
errors, not crashes.

### 0.0.13 — Resumable uploads
Scope: tus endpoint `/api/v1/uploads/` (A6); completion → ingest pipeline;
24h expiry of incomplete uploads; CLI uses tus automatically above 20 MiB
with progress on stderr.
Done when: a 500 MiB upload interrupted at ~50% (kill the CLI) resumes on
re-run without re-sending the first half and produces a correct item;
abandoned partial uploads are cleaned after 24h.

### 0.0.14 — End-to-end encrypted pastes
Scope: A9 in full: CLI `--encrypt` (+ optional password), browser decryption
page, client-side highlighting, encrypted file download.
Done when: server DB and blob contain no plaintext (test greps for a marker
string); link with `#k=` decrypts in the browser; wrong/missing key shows a
clear error; encrypted items are excluded from search and OpenGraph.

### 0.0.15 — Chrome extension
Scope: `extensions/chrome/` per A12; client generated/typed from OpenAPI;
`cors_origins` documented; tus for large files.
Done when (manual + Playwright where feasible): right-click image → URL in
clipboard; selected text → paste URL; screenshot → image URL; popup lists
recent items; works against a server with only the extension's origin in
`cors_origins`.

### 0.0.16 — Android one-tap send
Scope: `android/` per A12: share target, WorkManager + tus, notification
actions, multi-image → collection, Quick Settings tile, settings screen.
Done when (manual on a device): sharing one photo from Gallery puts a URL in
the clipboard within seconds on Wi-Fi; sharing 5 photos yields one collection
URL; turning on airplane mode mid-upload and off again completes the upload;
HEIC photos display correctly via the server.

### 0.0.17 — adseen integration
Scope: review adseen's code; agree the contract (token, `source=adseen`,
`meta.adseen_id`, batch → collection, list by `source` for reconciliation);
implement the adseen side (in the adseen repo) and any tibi gaps.
Done when: publishing an asset from adseen yields a tibi URL stored back in
adseen; `GET /api/v1/items?source=adseen` reconciles with adseen's records.

### 0.1.0 — Feature-complete release
Scope: no new features. Full regression on fs + s3; security review of A8 and
A9 (fresh-eyes review by a separate agent/person); docs complete (README with
the 题壁 note, DEPLOY, API, CLIENTS); `LICENSE` added per owner's decision;
CHANGELOG summarized; public repository and announcement.
Done when: every "done when" item from 0.0.1–0.0.17 passes in CI or has a
recorded manual check; the owner has signed off on the license.

---

### Version map (summary)

| version | theme |
|---|---|
| 0.0.1 | skeleton: upload any file, get a link |
| 0.0.2 | image hosting core |
| 0.0.3 | pastebin core |
| 0.0.4 | management, expiry, private, slugs, burn |
| 0.0.5 | S3 / Backblaze B2 storage |
| 0.0.6 | video, audio, HEIC, thumbnails |
| 0.0.7 | search + OpenAPI contract |
| 0.0.8 | collections + forks |
| 0.0.9 | web upload, OpenGraph, share links, i18n |
| 0.0.10 | cost control, metrics, export/import |
| 0.0.11 | packaging + ShareX/Flameshot/PicGo |
| 0.0.12 | MCP server |
| 0.0.13 | resumable uploads (tus) |
| 0.0.14 | end-to-end encrypted pastes |
| 0.0.15 | Chrome extension |
| 0.0.16 | Android app |
| 0.0.17 | adseen integration |
| 0.1.0 | feature-complete release |

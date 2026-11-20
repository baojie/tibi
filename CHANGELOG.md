# Changelog

All notable changes to this project are documented here. The project follows
[Semantic Versioning](https://semver.org/); before 1.0 the API may change
between minor versions, but the public URL scheme is frozen from 0.0.1.

## [0.0.10] — Operations: cost control, metrics, export

### Added
- Traffic counters: `view_count` (view pages and first-range raw hits) and
  `bytes_served` per item, plus per-day totals, kept in memory and written
  in batches every 10 seconds and at shutdown.
- Daily bandwidth caps per item and per instance
  (`max_item_bytes_per_day`, `max_total_bytes_per_day`): raw, download and
  thumbnail URLs answer `429` with `Retry-After` until midnight UTC; the
  view page explains (translated).
- Hotlink protection for images, video and audio (`allowed_referers`,
  with `*.example.com` wildcards); empty referers and tibi's own pages are
  always allowed.
- Per-token upload quotas (`--quota`), enforced on uploads, multipart
  uploads and forks with a `413` that states the numbers; `whoami` reports
  `used_bytes`.
- `/metrics` in Prometheus format (admin token, or a direct local
  connection; never through a proxy), `GET /api/v1/stats` (admin) and
  `tibi admin stats`.
- `tibi admin export` / `tibi admin import`: a `.tar.zst` with live items,
  collections, tokens, blobs, thumbnails and the share-link key; import
  into an empty instance (any backend) reproduces every URL byte for byte,
  verifies each blob's hash and rebuilds the search index.
- `docs/DEPLOY.md`: limits, monitoring, and backups with Litestream.

## [0.0.9] — Web experience

### Added
- `/upload`: the owner's upload page. Connect once with an API token (kept
  in the browser's local storage, verified with `whoami`), then drag and
  drop, pick files, or paste screenshots/images straight from the
  clipboard; type or paste text pastes with an optional language. Options:
  expiry, private, burn after reading, upload as one collection. Results
  with copy buttons; upload progress.
- `/recent`: newest items (or search results) with thumbnails, select all,
  bulk delete, load more. Private thumbnails load with the token.
- OpenGraph and Twitter card tags on view and collection pages, so links
  unfurl in Slack, Discord, Telegram, etc. — never for private,
  burn-after-read or encrypted items (nor through share links).
- Signed share links: `POST /api/v1/items/{id}/share-link?ttl=1h` and
  `tibi share ID [--ttl]` return an HMAC-signed URL that opens a private
  item (page, raw, download, thumbnail) until it expires, never past the
  item's own expiry. The key is created on first start
  (`secret_key_file`, default `data_dir/secret.key`, mode 0600).
- Internationalized UI: every page string lives in `web/i18n/*.toml`;
  English and Simplified Chinese ship. The locale follows
  `Accept-Language` (fallback `default_locale`); page scripts get their
  strings through a data attribute (no inline scripts). A test keeps the
  catalogs complete.
- The Fork button on text pages now appears in browsers connected on the
  upload page.
- A real-browser end-to-end test (Chrome via the DevTools protocol, no
  dependencies) pastes an image into `/upload` and checks the resulting
  link; it runs in CI.

## [0.0.8] — Collections and forks

### Added
- Collections (albums and multi-file pastes) sharing the item ID namespace:
  `POST/GET /api/v1/collections`, `GET/PATCH/DELETE /api/v1/collections/{id}`
  (title, visibility, expiry, reorder by listing item IDs). Deleting a
  collection deletes its items.
- Uploads with `collection=new` (plus `collection_title`) put all files
  into a new collection in order; `collection=<id>` appends. The response
  is `{"collection": …, "items": […]}`. Items carry `collection_id`,
  `collection_url` and `position`; `GET /api/v1/items?collection=` filters.
- `/c/{id}` page: a thumbnail grid for all-image albums, otherwise every
  item stacked in order (images, players, highlighted code with per-file
  line links such as `#f2-L20-L24`, rendered Markdown). Private
  collections need a `read_private` token.
- Forks: `POST /api/v1/items/{id}/fork` creates a new version with
  `forked_from` (title, filename, lang and visibility default to the
  original's); the original never changes. Item pages link to what they
  were forked from and to their collection. Text pages have a Fork editor
  for browsers that hold an API token (stored by the upcoming web upload
  page).
- CLI: `tibi up --album FILE...` prints one collection URL; `tibi fork ID
  [FILE|-]` (opens `$EDITOR` prefilled with the original when no input is
  given); `tibi rm` accepts collection URLs.

### Fixed
- Copy and fork buttons fetch raw text from the page's own origin, so they
  also work when `raw_base_url` is a separate domain.

## [0.0.7] — Search and API contract

### Added
- Full-text search over titles, filenames and text content (first 1 MiB):
  `GET /api/v1/search?q=…` and `tibi search`. Every word must match; words
  of three or more characters match anywhere (identifier substrings, via an
  FTS5 trigram index); Chinese, Japanese and Korean match any substring,
  including one or two characters (CJK bigrams); case-insensitive. Results
  are newest first, paginated, with a snippet of the matching line. Deleted,
  expired and encrypted items never appear.
- Existing items are indexed automatically when the server starts.
- `api/openapi.yaml` now documents every endpoint and response. The test
  suite validates every API response against it (types, required fields,
  undocumented fields, status codes, content types) and fails if any
  documented response is never exercised.
- `docs/API.md`, generated from the spec (`go run ./tools/apidoc`); a test
  fails when it is out of date.

## [0.0.6] — Media: video, audio, HEIC, thumbnails

### Added
- `video` (MP4, WebM, MOV) and `audio` (MP3, Ogg, Opus, M4A, WAV, FLAC) kinds
  with inline players; seeking uses `Range`. `duration_ms` for MP4/MOV/M4A
  and WAV (read from the container header, also when `moov` is at the end).
- HEIC/HEIF photos are converted to JPEG with an external tool —
  `heic_converter = "auto"` tries `vips`, `heif-convert`, `magick` — or a
  custom command path. The original type is kept as `orig_mime`; the
  converted JPEG has its metadata (and orientation, already applied to the
  pixels) removed. Without a converter, or when conversion fails, the photo
  is stored as-is and offered for download with a note.
- WebP thumbnails (400px wide, EXIF orientation applied) for PNG, JPEG, GIF
  and WebP images, generated on upload, shared between identical images,
  served at `/{id}/thumb` (`thumbnail_url`) and garbage-collected with their
  image. Encoding is pure Go (libwebp via purego when present, WASM
  otherwise).

### Changed
- Matroska (`.mkv`) files are labelled `video/x-matroska` instead of
  `video/webm` and stored as files.

## [0.0.5] — Object storage: S3 / Backblaze B2

### Added
- `s3` blob backend for any S3-compatible store (AWS S3, Backblaze B2,
  Cloudflare R2, MinIO, …) via minio-go: private objects with type and
  immutable caching headers, seekable reads for `Range`.
- Startup probe (write, read, delete) that fails fast on misconfiguration.
- `blob_delivery = "redirect"`: 302 to one-hour presigned URLs for passive
  media and downloads; text, active types, private and burn-after-read items
  stay proxied.
- `tibi admin blobs migrate --to s3|fs`: verified, idempotent, resumable copy
  of all referenced blobs; raw URLs are unchanged.
- `tibi admin gc --orphans`: deletes stored blobs no item references (older
  than an hour).
- The server refuses a world-readable config file that contains S3 keys.
- `docs/DEPLOY.md` (configuration, S3/B2 setup, delivery modes, migration).
- CI runs the whole suite a second time against an S3-compatible server;
  `scripts/test-s3.sh` does the same locally. Verified manually against a
  real Backblaze B2 bucket.

### Fixed
- Deleting a blob from a versioned bucket (Backblaze B2's default) removes
  all of its versions; previously only a delete marker was added and the
  bytes stayed stored and billed.
- Expiry times are rounded up to the next second, so an item never expires
  before its requested lifetime (a `1s` expiry could previously last ~0s).

## [0.0.4] — Item management and lifecycle

### Added
- `GET /api/v1/items` (newest first, cursor pagination, `kind`/`source`
  filters), `GET`, `PATCH` (title, lang, visibility, expires, slug — never
  content) and `DELETE /api/v1/items/{id}` (scope `delete`, soft delete).
- Expiry: `expires` on upload or patch (`10m`, `1h`, `7d`, `30d`, `never`,
  …); server `default_expires` and client `default_expires` defaults.
  Expired items 404 at once.
- Garbage collection every 10 minutes (and `tibi admin gc`): expired items
  and items deleted more than a day ago are purged, then blobs nothing
  references. Uploads and GC are serialized per blob so a blob is never
  deleted under a concurrent upload of the same content.
- Private items: served only to requests with a `read_private` token; 404
  otherwise, indistinguishable from missing items. `Cache-Control:
  private, no-store`.
- Custom slugs: `/s/{slug}` redirects to the item; slugs are pointers that
  move on reassignment; reserved words are rejected; deletion releases them.
- Burn after reading: unprivileged visitors get a "reveal" page (link
  previewers' GETs and raw prefetches consume nothing); the POST reveal shows
  the item once (images inlined, files downloaded) and deletes it. Concurrent
  reveals: exactly one succeeds.
- Raw responses of expiring items are cacheable only until expiry.
- View pages show expiry, private and burn notices.
- CLI: `tibi ls`, `info`, `rm`, `open`, `slug`; upload flags `-e/--expire`,
  `-p/--private`, `--slug`, `--burn`. Commands accept item IDs or URLs.

### Fixed
- Items uploaded within the same second now list in upload order.

## [0.0.3] — Pastebin core

### Added
- Text view page: server-side syntax highlighting (chroma) with line numbers,
  `#L12` and `#L12-L20` line links (shift-click selects a range), wrap
  toggle, copy-all, raw and download. Light and dark themes.
- Markdown pastes render (GFM, highlighted code blocks) with a
  rendered/source toggle; raw HTML and dangerous link schemes are dropped.
- Language inference: `lang` option > file extension > content (shebangs,
  JSON, diffs, Go, Python, Rust, C, JS, SQL, Dockerfile, Makefile, Markdown,
  …) > plain text. Item responses include `lang`; pastes without a filename
  get a matching extension (`/{id}.py`).
- Charset detection and transcoding to UTF-8 for GBK/GB18030, Big5,
  Shift_JIS, EUC-JP, EUC-KR, Latin-1 and UTF-16 with BOM; the original charset
  is recorded as `charset` and shown on the view page. Raw downloads serve the
  transcoded UTF-8 (the original bytes are not kept).
- Large text: the view shows the first `max_text_bytes` (default 2 MiB) with
  a link to the full raw text; highlighting applies up to 256 KiB.
- Rendered text views are cached in memory (content is immutable).
- CLI: `tibi paste [-l LANG] [-t TITLE]` reads stdin or opens `$EDITOR`;
  `-l/--lang` on all upload commands.

## [0.0.2] — Image hosting core

### Added
- Content sniffing and kind classification: PNG, JPEG, GIF, WebP and AVIF
  are `image`; valid UTF-8 text (no NUL) is `text`; SVG, HTML, XHTML and
  JavaScript are always `file`; everything else is `file`. Clients may force
  `kind=text|file`.
- Multipart uploads: several `file` parts in one request return an array.
  Form fields may appear before or after the files.
- Lossless metadata stripping for JPEG (EXIF incl. GPS, XMP, IPTC, comments,
  trailing MPF images), PNG (`eXIf`, `tEXt`, `zTXt`, `iTXt`, `tIME`) and WebP
  (`EXIF`, `XMP `). JPEG orientation is preserved as a minimal EXIF block.
  Opt out per upload with `keep_meta=1` or globally with
  `strip_image_meta = false`.
- Image dimensions (`width`, `height`, display-oriented) and a `markdown`
  field in item responses.
- Image view page with the picture and copy buttons for link, direct URL and
  Markdown (external script, no inline JS).
- CLI: `--md`, `--raw`, `--keep-meta`, `--no-copy`; results are copied to the
  clipboard when stdout is a terminal (Wayland, X11, macOS); `tibi clip`
  uploads the clipboard, preferring an image over text.

### Changed
- Images always use the extension of their detected type in `raw_url`
  (a PNG named `shot.jpg` is served at `/{id}.png`).

## [0.0.1] — Walking skeleton

### Added
- Single `tibi` binary: `tibi serve`, `tibi admin`, and the client CLI.
- Server config from TOML (`/etc/tibi/server.toml` or `--config`) with
  `TIBI_*` environment overrides.
- SQLite metadata store (pure Go, WAL) with forward-only embedded migrations.
- Content-addressed `fs` blob store (`ab/cd/<sha256>`, atomic writes, dedup).
- API tokens: `tibi admin token create|list|revoke`; tokens are stored hashed
  and scoped (`upload`, `read_private`, `delete`, `admin`).
- `POST /api/v1/items` raw-body upload (options via query or `X-Tibi-*`
  headers), stored as kind `file`; `GET /api/v1/whoami`;
  `GET /api/v1/openapi.yaml`; `GET /healthz`.
- Frozen public URL scheme: `/{id}`, `/{id}.{ext}`, `/{id}/raw`, `/{id}/dl`.
- Security headers: active content (HTML, SVG, XHTML, JS, XML) is never served
  with its real type; `nosniff` everywhere; sandbox CSP on raw routes, strict
  CSP on view pages; `X-Robots-Tag: noindex` on every response and a
  disallow-all `/robots.txt`.
- Size limit (`max_upload_bytes`) enforced while streaming → 413.
- CLI: `tibi login`, `tibi up FILE...`, `tibi up -`, and `<cmd> | tibi`;
  stdout carries only URLs, `--json` prints API responses.
- `api/openapi.yaml` contract (started) and CI (gofmt, vet, build, race tests).

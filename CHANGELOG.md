# Changelog

All notable changes to this project are documented here. The project follows
[Semantic Versioning](https://semver.org/); before 1.0 the API may change
between minor versions, but the public URL scheme is frozen from 0.0.1.

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

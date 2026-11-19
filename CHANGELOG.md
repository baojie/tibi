# Changelog

All notable changes to this project are documented here. The project follows
[Semantic Versioning](https://semver.org/); before 1.0 the API may change
between minor versions, but the public URL scheme is frozen from 0.0.1.

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

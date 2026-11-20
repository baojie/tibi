# Deploying tibi

This guide grows with each release. It currently covers configuration and
blob storage (local disk or S3-compatible object storage).

## Installing

Release builds (once published) provide:

- **Binaries** for Linux (amd64, arm64), macOS (arm64) and Windows (amd64,
  for the client), as archives with the docs and client integrations.
- **A `.deb`** that installs `/usr/bin/tibi`, a hardened systemd unit, a
  `tibi` system user, `/var/lib/tibi` and `/etc/tibi/server.toml`:

  ```sh
  sudo apt install ./tibi_<version>_linux_amd64.deb   # pulls libvips-tools for HEIC
  sudoedit /etc/tibi/server.toml                       # at least base_url
  sudo systemctl enable --now tibi
  sudo -u tibi tibi admin token create --name me --scopes upload,delete,read_private --config /etc/tibi/server.toml
  ```

  Secrets (S3 keys) go in `/etc/tibi/env` (mode 0600), read by the unit.
  Removing the package keeps your data and the `tibi` user.
- **A Docker image** (`ghcr.io/<owner>/tibi`, amd64 and arm64) with the
  HEIC converter included:

  ```sh
  docker run -d --name tibi -p 127.0.0.1:8070:8070 \
    -v tibi-data:/data -e TIBI_BASE_URL=https://tibi.example.com ghcr.io/<owner>/tibi
  docker exec tibi tibi admin token create --name me --scopes upload,delete,read_private
  ```

  The server runs as uid 10001; a bind-mounted data directory must be
  writable by it (`chown 10001 /srv/tibi-data`). `docker build -t tibi .`
  builds the same image from source.

Put a reverse proxy with TLS in front (Caddy, nginx); with nginx raise
`client_max_body_size` to at least `max_upload_bytes` (resumable uploads
send 16 MiB chunks, so that also covers them).

**Large files** use resumable uploads (tus) at `/api/v1/uploads/`, up to
`max_resumable_bytes` (default 2 GiB). Partial uploads live in
`data_dir/uploads` (same filesystem as `data_dir/tmp`, so finished files are
moved, not copied) and are removed after 24 hours if never finished.

## Configuration

The server reads `/etc/tibi/server.toml` (or `--config PATH`, or
`TIBI_CONFIG`). Every scalar setting can be overridden with a `TIBI_*`
environment variable (e.g. `TIBI_BASE_URL`, `TIBI_DATA_DIR`).

```toml
listen = "127.0.0.1:8070"
base_url = "https://tibi.example.com"
data_dir = "/var/lib/tibi"          # SQLite DB, temp files, and fs blobs
default_expires = "never"
max_upload_bytes = 52428800
blob_delivery = "proxy"             # proxy | redirect (see below)

[storage]
backend = "fs"                      # fs | s3
```

The metadata database is always local (`data_dir/tibi.db`), even when blobs
live in object storage.

## Web pages and the signing key

`/upload` and `/recent` are the owner's pages. Open `/upload`, paste an API
token once, and the browser keeps it in local storage (on that origin
only). Anyone can load these pages, but they show nothing without a valid
token. Consider serving raw content from a separate origin
(`raw_base_url`) for defence in depth.

Share links for private items are signed with a key the server creates on
first start at `data_dir/secret.key` (or `secret_key_file`), mode 0600.
Back it up with the database; replacing it invalidates every share link
issued so far (a quick way to revoke them all).

## Blob storage

### Local disk (`fs`, default)

Blobs are stored under `data_dir/blobs/ab/cd/<sha256>`. Nothing else to set.

### S3-compatible object storage (`s3`)

One implementation covers AWS S3, Backblaze B2 (S3-compatible API),
Cloudflare R2, Wasabi and self-hosted servers. Objects are written private,
with their `Content-Type` and `Cache-Control: public, max-age=31536000,
immutable`.

```toml
[storage]
backend = "s3"

[storage.s3]
endpoint = "s3.us-west-004.backblazeb2.com"   # host[:port]; prefix http:// for plain HTTP
region = "us-west-004"
bucket = "tibi-blobs"
prefix = "blobs/"                             # optional key prefix
use_path_style = false                        # true for MinIO / most self-hosted servers
```

**Credentials** should come from the environment, not the file:

```sh
TIBI_S3_ACCESS_KEY=...
TIBI_S3_SECRET_KEY=...
```

If you do put `access_key` / `secret_key` in the config file, the server
refuses to start while that file is world-readable (`chmod 600` it).

On startup with `s3`, tibi writes, reads and deletes a small probe object
(`<prefix>_probe/…`) and exits with a clear error if any step fails, so a
wrong endpoint, bucket or key shows up immediately.

| provider | endpoint | region | notes |
|---|---|---|---|
| AWS S3 | `s3.amazonaws.com` or `s3.<region>.amazonaws.com` | bucket region | |
| Backblaze B2 | `s3.<region>.backblazeb2.com` (shown on the bucket page) | e.g. `us-west-004` | use an *application key* limited to the bucket |
| Cloudflare R2 | `<account-id>.r2.cloudflarestorage.com` | `auto` | |
| MinIO / versitygw / SeaweedFS | `http://host:port` | `us-east-1` | `use_path_style = true` |

#### Versioned buckets (Backblaze B2 default)

B2 buckets keep every version of an object by default, and AWS buckets may
have versioning enabled. A plain S3 `DELETE` there only hides the object
behind a delete marker, while the bytes stay stored and billed. tibi
therefore deletes **every version** of a blob explicitly when GC removes it,
so nothing is left behind. Setting the bucket's lifecycle to "Keep only the
last version" is still a good belt-and-braces measure.

#### Backblaze B2 step by step

1. Create a **private** bucket (e.g. `tibi-blobs`). Note the S3 endpoint
   shown on the bucket page, e.g. `s3.us-west-004.backblazeb2.com`; the region
   is the part after `s3.` (`us-west-004`).
2. Create an application key with read and write access to that bucket only.
   The *keyID* is the access key, the *applicationKey* the secret key.
3. Configure tibi as above and start it. A successful start means the probe
   passed.
4. Upload a file and fetch its raw URL to confirm.

Verified against a real B2 bucket (region `us-east-005`) for 0.0.5: the whole
test suite passes with blobs in B2, `redirect` delivery serves presigned B2
URLs (including `Range`), and delete + GC leave no object versions behind.

### Delivery: `proxy` or `redirect`

- `proxy` (default): tibi streams every blob itself and applies all its
  security headers.
- `redirect`: raw image/video/audio URLs and `/dl` downloads answer with a
  302 to a presigned object-store URL valid for one hour (or until the item
  expires, if sooner), saving tibi's bandwidth. Text and anything that could
  execute in a browser (HTML, SVG, JavaScript, …) are always proxied, as are
  private and burn-after-read items. With the `fs` backend, `redirect`
  behaves like `proxy`.

`Range` requests work in both modes.

### Moving between backends

```sh
tibi admin blobs migrate --to s3      # copies from the configured backend
# then set storage.backend = "s3" and restart
```

The migration copies every blob any item references, re-reads each copy to
verify its SHA-256, and skips blobs already present, so it is safe to
interrupt and re-run. Raw URLs do not change. The source is left untouched;
delete it yourself once you are satisfied.

### Garbage collection

The server purges expired items and items deleted more than a day ago every
10 minutes, then deletes blobs no item references. `tibi admin gc` runs one
pass now; `tibi admin gc --orphans` also scans the store for blobs with no
database row (left by a crash between storing a blob and recording it) and
deletes those older than an hour.

## Limits and cost control

```toml
max_upload_bytes = 52428800        # one request
max_item_bytes_per_day = 0         # per item, 0 = unlimited
max_total_bytes_per_day = 0        # whole instance, 0 = unlimited
allowed_referers = []              # e.g. ["blog.example.com", "*.example.org"]
```

- **Daily bandwidth caps.** Bytes served by raw, download and thumbnail
  URLs are counted per UTC day (with `redirect` delivery, a redirect counts
  as the whole object, since the object store bills that egress). When an
  item or the instance reaches its cap, those URLs answer `429` with
  `Retry-After` until midnight UTC; view pages still load and explain.
  Useful with S3/B2, whose egress costs money.
- **Hotlink protection.** With `allowed_referers` set, images, video and
  audio requested from pages on other sites answer `403`. Requests without
  a `Referer` (direct visits, chat apps, most apps) and tibi's own pages
  are always allowed; downloads are not affected.
- **Per-token quota.** `tibi admin token create … --quota 10GiB` limits the
  total size of a token's live items; uploads beyond it answer `413` with
  the numbers. `whoami` reports `used_bytes`.

Counters (`view_count`, `bytes_served`, daily totals) are kept in memory
and written to the database every 10 seconds and at shutdown.

## Monitoring

- `GET /healthz` — liveness (checks the database).
- `GET /metrics` — Prometheus text format: requests by method and status,
  upload/served bytes, GC activity, items by kind, storage used, today's
  traffic. Allowed with an admin token, or on a direct connection from the
  same machine. Requests forwarded by a reverse proxy never count as local,
  so a public `/metrics` needs the token:

  ```yaml
  scrape_configs:
    - job_name: tibi
      authorization: { credentials: tibi_… }   # an admin-scoped token
      static_configs: [{ targets: ["tibi.example.com"] }]
      scheme: https
  ```
- `tibi admin stats` (or `GET /api/v1/stats` with an admin token) —
  storage, items, traffic today and in total, most-served items.

## Backups

**The database** (`data_dir/tibi.db`, SQLite in WAL mode) is best backed up
continuously with [Litestream](https://litestream.io), which streams the
WAL to S3/B2 and restores to any point in time. tibi does not embed it; run
it next to tibi:

```yaml
# /etc/litestream.yml
dbs:
  - path: /var/lib/tibi/tibi.db
    replicas:
      - type: s3
        endpoint: https://s3.us-west-004.backblazeb2.com
        bucket: tibi-db-backup
        path: tibi.db
```

```sh
litestream replicate -config /etc/litestream.yml     # alongside tibi serve
litestream restore -config /etc/litestream.yml /var/lib/tibi/tibi.db   # on a new machine
```

Back up `data_dir/secret.key` too (share links depend on it). Blobs on
S3/B2 are already off the machine; blobs on local disk need their own copy
(e.g. `restic` or `rsync` of `data_dir/blobs`).

**Full export.** `tibi admin export --out tibi-export.tar.zst` writes every
live item with its metadata, the collections, the tokens (hashed) and the
share-link key into one archive; `tibi admin import tibi-export.tar.zst`
restores it into an **empty** instance (with any storage backend) so that
every URL — raw bytes, short links, collections, share links — works as
before. Restart the server after an import. The archive contains private
content: store it like the database.

## HEIC photos (iPhone, many Android phones)

Browsers cannot show HEIC, and there is no mature pure-Go decoder, so tibi
converts HEIC/HEIF uploads to JPEG with an external tool:

```toml
convert_heic = true
heic_converter = "auto"   # auto | vips | heif-convert | magick | off | /path/to/command
```

`auto` uses the first of `vips`, `heif-convert`, `magick` found in `PATH`
(the server logs which one at startup). A custom command is run as
`command INPUT.heic OUTPUT.jpg`. Each conversion has a 60 s timeout.

The tools need libheif **with an HEVC decoder plugin**. On Debian/Ubuntu:

```sh
apt install libheif-examples libheif-plugin-libde265   # heif-convert
# or: apt install libvips-tools                         # vips
```

ImageMagick may list HEIC as readable while lacking the HEVC plugin; such
uploads then fail to convert and are stored as-is (a warning is logged).
Without a working converter, HEIC photos are kept unchanged and their page
offers a download.

## Development: testing against S3

`scripts/test-s3.sh` starts a throwaway S3-compatible server
(versitygw in Docker) on `127.0.0.1:19000` and runs the whole test suite
with blobs in it. Any S3 endpoint works via `TIBI_TEST_S3_ENDPOINT`,
`TIBI_TEST_S3_ACCESS_KEY`, `TIBI_TEST_S3_SECRET_KEY` and
`TIBI_TEST_S3_BUCKET`.

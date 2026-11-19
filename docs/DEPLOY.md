# Deploying tibi

This guide grows with each release. It currently covers configuration and
blob storage (local disk or S3-compatible object storage).

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

## Development: testing against S3

`scripts/test-s3.sh` starts a throwaway S3-compatible server
(versitygw in Docker) on `127.0.0.1:19000` and runs the whole test suite
with blobs in it. Any S3 endpoint works via `TIBI_TEST_S3_ENDPOINT`,
`TIBI_TEST_S3_ACCESS_KEY`, `TIBI_TEST_S3_SECRET_KEY` and
`TIBI_TEST_S3_BUCKET`.

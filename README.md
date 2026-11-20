# tibi

**tibi** (题壁) is named after the Tang/Song practice of writing poems on public
walls for any passerby to read. It is a self-hosted image host and pastebin:
push any image, code snippet, or file from any program and get back a URL you
can share.

Status: early development (see `CHANGELOG.md`). The design and roadmap live in
`PLAN.md`.

Install: release binaries, a `.deb` with a systemd unit, or the Docker
image — see `docs/DEPLOY.md`. Upload from the CLI, curl, the web page,
ShareX, Flameshot or PicGo — see `docs/CLIENTS.md`.

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

## URLs

| URL | meaning |
|---|---|
| `/{id}` | view page |
| `/{id}.{ext}` | raw bytes (direct embed) |
| `/{id}/raw` | raw bytes |
| `/{id}/dl` | download |

The API contract is `api/openapi.yaml`, also served at `/api/v1/openapi.yaml`;
a readable version is `docs/API.md`.

Deployment, storage backends (local disk, S3, Backblaze B2) and migration:
`docs/DEPLOY.md`.

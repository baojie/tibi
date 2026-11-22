# tibi API 0.1.4

<!-- Generated from api/openapi.yaml by `go run ./tools/apidoc`. Do not edit. -->

tibi (题壁) is a self-hosted image host and pastebin: upload an image, a
text or code snippet, or any file, and get back a URL to share.

This document is the source of truth for `/api/v1`. It is served at
`/api/v1/openapi.yaml`, and the test suite checks every API response
against it.

**Authentication.** Send `Authorization: Bearer tibi_…`. Tokens carry
scopes: `upload` (upload, list, search, view metadata, change metadata),
`delete`, `read_private` (see private items) and `admin` (all of them).

**Private items** are visible through this API only to tokens with
`read_private` and to the token that uploaded them; to any other token
they do not exist (404, absent from lists and search, hidden inside
collections). Their public URLs need a `read_private` token or a signed
share link. So a token kept on a
phone or in a browser extension with just `upload` cannot read the
owner's private items if it leaks.

**Rate limit.** Each token may make `rate_limit` requests per minute
(default 1200), in bursts of up to `rate_burst` (default 200); beyond
that the API answers `429` with `Retry-After`.

**Errors** use one envelope: `{"error": {"code": "…", "message": "…"}}`.

**Web pages.** `/upload` and `/recent` are the owner's pages; they keep
an API token in the browser's local storage and call this API.

**Limits.** Raw content answers `429` (with `Retry-After`) once an item
or the whole instance used up its daily bandwidth allowance; view pages
stay available and explain. With `allowed_referers` set, images, video
and audio embedded by other sites answer `403`. Uploads beyond a
token's quota answer `413`.

**Public URLs** (not part of this API) are fixed forever:
`/{id}` view page, `/{id}.{ext}` raw bytes for embedding, `/{id}/raw`,
`/{id}/dl` download, `/{id}/thumb` image thumbnail, `/s/{slug}` short
link, `/c/{id}` collection. Raw routes support `Range`.

**Collections** group items in order: an album (all images: shown as a
thumbnail grid) or a multi-file paste (shown stacked). Item and
collection IDs share one namespace.

**CORS.** Browser clients on other origins (such as the Chrome
extension) can call the API when the server lists their origin in
`cors_origins`; preflights are answered, and the tus headers
(`Location`, `Upload-Offset`, …) are exposed.

**Encryption.** Items of kind `encrypted` are encrypted by the client;
the server never sees the key, which travels in the link's fragment
(`/{id}#k=<base64url key>`; browsers do not send fragments). Format v1:
byte 0 = 1 (version), byte 1 = flags (bit 0: password), then for a
password a 16-byte PBKDF2 salt and 4-byte big-endian iteration count,
a 12-byte nonce, and AES-256-GCM ciphertext with all preceding bytes as
additional data. AES key = HKDF-SHA256(K, info "tibi-e2e-v1"), or
HKDF-SHA256(K ‖ PBKDF2-SHA256(password)) with a password. The plaintext
is a 4-byte big-endian length, a JSON header (`name`, `type`, `lang`),
and the content. The view page decrypts in the browser.

**Content is immutable.** To "edit" an item, fork it: the new item has
`forked_from` set and the original stays as it was.

## Endpoints

| method | path | summary |
|---|---|---|
| `GET` | [`/api/v1/items`](#get-api-v1-items) | List items, newest first |
| `POST` | [`/api/v1/items`](#post-api-v1-items) | Upload one or more items |
| `GET` | [`/api/v1/items/{id}`](#get-api-v1-items-id) | Item metadata |
| `PATCH` | [`/api/v1/items/{id}`](#patch-api-v1-items-id) | Change metadata |
| `DELETE` | [`/api/v1/items/{id}`](#delete-api-v1-items-id) | Delete an item |
| `POST` | [`/api/v1/items/{id}/fork`](#post-api-v1-items-id-fork) | Fork an item (new version) |
| `POST` | [`/api/v1/items/{id}/share-link`](#post-api-v1-items-id-share-link) | Signed, time-limited link |
| `GET` | [`/api/v1/search`](#get-api-v1-search) | Full-text search |
| `GET` | [`/api/v1/collections`](#get-api-v1-collections) | List collections, newest first |
| `POST` | [`/api/v1/collections`](#post-api-v1-collections) | Create a collection |
| `GET` | [`/api/v1/collections/{id}`](#get-api-v1-collections-id) | A collection and its items |
| `PATCH` | [`/api/v1/collections/{id}`](#patch-api-v1-collections-id) | Change a collection or reorder its items |
| `DELETE` | [`/api/v1/collections/{id}`](#delete-api-v1-collections-id) | Delete a collection and its items |
| `GET` | [`/api/v1/openapi.yaml`](#get-api-v1-openapi-yaml) | This document |
| `GET` | [`/api/v1/stats`](#get-api-v1-stats) | Instance statistics |
| `POST` | [`/api/v1/uploads/`](#post-api-v1-uploads) | Start a resumable upload (tus) |
| `PATCH` | [`/api/v1/uploads/{id}`](#patch-api-v1-uploads-id) | Send bytes (tus) |
| `DELETE` | [`/api/v1/uploads/{id}`](#delete-api-v1-uploads-id) | Abandon an upload (tus) |
| `GET` | [`/api/v1/uploads/{id}/item`](#get-api-v1-uploads-id-item) | The item a finished upload became |
| `GET` | [`/api/v1/whoami`](#get-api-v1-whoami) | Describe the calling token |
| `GET` | [`/healthz`](#get-healthz) | Liveness probe |
| `GET` | [`/metrics`](#get-metrics) | Prometheus metrics |

<a id="get-api-v1-items"></a>
### `GET /api/v1/items`

**List items, newest first**

Live items (not deleted or expired), including private ones. Requires scope `upload`.

| parameter | in | type | description |
|---|---|---|---|
| `limit` | query | integer |  |
| `cursor` | query | string | The `next_cursor` of the previous page. |
| `kind` | query | [Kind](#schema-kind) |  |
| `source` | query | string | Only items uploaded with this `source` tag. |
| `collection` | query | string | Only items of this collection. |

| status | meaning | body |
|---|---|---|
| 200 | A page of items. | `application/json` [ItemPage](#schema-itempage) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="post-api-v1-items"></a>
### `POST /api/v1/items`

**Upload one or more items**

Requires scope `upload`. Two request forms:

- **multipart/form-data** — one or more `file` parts, plus optional
  form fields with the same names as the query parameters below.
  Fields may come before or after the files. Each part's filename is
  used. One file returns an Item; several return an array, in order.
  At most 100 `file` parts and 50 other fields per request.
- **raw body** — the body is the content. Options are query
  parameters, or headers named `X-Tibi-` plus the parameter name in
  title case with dashes (`X-Tibi-Filename`, `X-Tibi-Keep-Meta`,
  `X-Tibi-Burn-After-Read`, …).

The kind is detected from the content:

| kind | formats |
|---|---|
| `image` | PNG, JPEG, GIF, WebP, AVIF; HEIC/HEIF converted to JPEG when the server has a converter (`orig_mime` keeps the original type) |
| `video` | MP4, WebM, MOV |
| `audio` | MP3, Ogg, Opus, M4A, WAV, FLAC |
| `text` | valid UTF-8 text; GBK/GB18030, Big5, Shift_JIS, EUC-JP, EUC-KR, Latin-1 and UTF-16 (with BOM) are converted to UTF-8 and `charset` records the original |
| `file` | everything else, and always SVG, HTML, XHTML and JavaScript (never rendered) |

EXIF (including GPS), XMP and text metadata are removed losslessly
from JPEG, PNG and WebP unless `keep_meta=1`; JPEG orientation is
kept. Identical content is stored once.

| parameter | in | type | description |
|---|---|---|---|
| `filename` | query | string | Original file name (multipart parts carry their own). |
| `title` | query | string |  |
| `kind` | query | string | Force `text` or `file` (media kinds cannot be forced), or `encrypted` for end-to-end encrypted content: the body must be in the tibi encrypted format (see `docs/API.md` → Encryption); it is stored as opaque bytes, never indexed or previewed, and any `filename` is dropped. `lang` is kept as a display hint. |
| `lang` | query | string | Highlighting language (name, alias or extension, e.g. `python`, `py`); inferred when omitted; unknown names are rejected. |
| `keep_meta` | query | string | Keep image metadata (EXIF, GPS, XMP). |
| `expires` | query | string | Lifetime, e.g. `10m`, `1h`, `7d`, `30d`, or `never`. Default is the server's `default_expires`. |
| `visibility` | query | string | `private` items are served only to tokens with `read_private`. |
| `slug` | query | string | Short link `/s/{slug}`: 3–64 characters of `a-z`, `0-9` and `-`, not a reserved word. Moves from any item that had it. Single-file uploads only. |
| `burn_after_read` | query | string | Delete the item when it is first revealed by a visitor without a `read_private` token. |
| `source` | query | string | Client tag, e.g. `cli`, `chrome`, `adseen`. |
| `source_url` | query | string (uri) | Where the content came from (an image's address, the page a text was taken from): an http(s) URL. Returned only to the uploading token and `read_private` tokens; never on public pages. |
| `meta` | query | string | Client metadata (a JSON object as text). |
| `collection` | query | string | `new` puts all uploaded files into a new collection, in order; a collection ID appends them to it. The response is then a CollectionUpload. Not allowed with `burn_after_read`. |
| `collection_title` | query | string | Title of the new collection (with `collection=new`). |

Request body: `application/octet-stream` (string (binary)), `multipart/form-data` (object)

| status | meaning | body |
|---|---|---|
| 201 | Created. An Item for one file, an array of Items for several, or a CollectionUpload when `collection` is set. | `application/json` [Item](#schema-item) or array of [Item](#schema-item) or [CollectionUpload](#schema-collectionupload) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 413 | The upload exceeds `max_upload_bytes`, or the token's upload quota (`message` gives the numbers). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="get-api-v1-items-id"></a>
### `GET /api/v1/items/{id}`

**Item metadata**

Any visibility. Requires scope `upload`.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

| status | meaning | body |
|---|---|---|
| 200 | The item. | `application/json` [Item](#schema-item) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="patch-api-v1-items-id"></a>
### `PATCH /api/v1/items/{id}`

**Change metadata**

Content is immutable. Only these fields can be set: `title`, `lang`
(text items), `visibility`, `expires` (relative to now, or `never`)
and `slug` (an empty string removes it). Any other field is
rejected. Requires scope `upload`; making the item expire sooner
than it would (including giving a never-expiring item an expiry)
deletes it early, so that also requires scope `delete`.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

Request body: `application/json` (object)

| status | meaning | body |
|---|---|---|
| 200 | The updated item. | `application/json` [Item](#schema-item) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="delete-api-v1-items-id"></a>
### `DELETE /api/v1/items/{id}`

**Delete an item**

The item returns 404 at once and is purged after a day. Requires scope `delete`.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

| status | meaning | body |
|---|---|---|
| 204 | Deleted. | — |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="post-api-v1-items-id-fork"></a>
### `POST /api/v1/items/{id}/fork`

**Fork an item (new version)**

The request body is the new content, as in a raw-body upload. The
new item gets `forked_from` = the original's ID; the original is
unchanged. `title`, `filename`, `lang` and `visibility` default to
the original's; forks of text stay text. Requires scope `upload`.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |
| `title` | query | string |  |
| `filename` | query | string |  |
| `lang` | query | string |  |
| `kind` | query | string |  |
| `visibility` | query | string |  |
| `expires` | query | string |  |

Request body: `application/octet-stream` (string (binary))

| status | meaning | body |
|---|---|---|
| 201 | The new item. | `application/json` [Item](#schema-item) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 413 | The upload exceeds `max_upload_bytes`, or the token's upload quota (`message` gives the numbers). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="post-api-v1-items-id-share-link"></a>
### `POST /api/v1/items/{id}/share-link`

**Signed, time-limited link**

Returns a view-page URL carrying `exp` and `sig` parameters (an
HMAC-SHA256 over the item ID and expiry, with a server secret).
Anyone with the link can view the item — typically a private one —
and its raw, download and thumbnail URLs until the link expires; it
never outlives the item. Requires scope `upload`.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |
| `ttl` | query | string | Lifetime, e.g. `10m`, `1h`, `7d`; at most `365d`. Default `1h`. |

| status | meaning | body |
|---|---|---|
| 200 | The signed URL. | `application/json` [ShareLink](#schema-sharelink) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="get-api-v1-search"></a>
### `GET /api/v1/search`

**Full-text search**

Finds live items whose title, filename or text contain **every**
word of `q`, newest first. Words of three or more characters match
anywhere, including inside identifiers (`Upload` finds
`handleUpload`); shorter words match word starts. Chinese, Japanese
and Korean match any substring, including one or two characters.
Matching ignores case. Encrypted items are never indexed. Requires
scope `upload`.

| parameter | in | type | description |
|---|---|---|---|
| `q` | query, required | string |  |
| `kind` | query | [Kind](#schema-kind) |  |
| `limit` | query | integer |  |
| `cursor` | query | string | The `next_cursor` of the previous page. |

| status | meaning | body |
|---|---|---|
| 200 | A page of matches. | `application/json` [SearchPage](#schema-searchpage) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="get-api-v1-collections"></a>
### `GET /api/v1/collections`

**List collections, newest first**

Requires scope `upload`.

| parameter | in | type | description |
|---|---|---|---|
| `limit` | query | integer |  |
| `cursor` | query | string | The `next_cursor` of the previous page. |

| status | meaning | body |
|---|---|---|
| 200 | A page of collections (without their items). | `application/json` [CollectionPage](#schema-collectionpage) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="post-api-v1-collections"></a>
### `POST /api/v1/collections`

**Create a collection**

Optionally gathers existing items, in the given order (items already in another collection are rejected). Requires scope `upload`.

Request body: `application/json` (object)

| status | meaning | body |
|---|---|---|
| 201 | The collection, with its items. | `application/json` [Collection](#schema-collection) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="get-api-v1-collections-id"></a>
### `GET /api/v1/collections/{id}`

**A collection and its items**

Requires scope `upload`.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

| status | meaning | body |
|---|---|---|
| 200 | The collection, with its live items in order. | `application/json` [Collection](#schema-collection) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="patch-api-v1-collections-id"></a>
### `PATCH /api/v1/collections/{id}`

**Change a collection or reorder its items**

`items` must list every item of the collection exactly once, in the new order. Requires scope `upload`.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

Request body: `application/json` (object)

| status | meaning | body |
|---|---|---|
| 200 | The updated collection, with its items. | `application/json` [Collection](#schema-collection) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="delete-api-v1-collections-id"></a>
### `DELETE /api/v1/collections/{id}`

**Delete a collection and its items**

The collection and every item in it are deleted (purged after a day). Requires scope `delete`.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

| status | meaning | body |
|---|---|---|
| 204 | Deleted. | — |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="get-api-v1-openapi-yaml"></a>
### `GET /api/v1/openapi.yaml`

**This document**

No authentication.

| status | meaning | body |
|---|---|---|
| 200 | The OpenAPI document. | `application/yaml` string |

<a id="get-api-v1-stats"></a>
### `GET /api/v1/stats`

**Instance statistics**

Storage, item counts, traffic and the most-served items. Requires scope `admin`.

| status | meaning | body |
|---|---|---|
| 200 | Statistics. | `application/json` [Stats](#schema-stats) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="post-api-v1-uploads"></a>
### `POST /api/v1/uploads/`

**Start a resumable upload (tus)**

[tus 1.0](https://tus.io/protocols/resumable-upload) creation.
Send `Tus-Resumable: 1.0.0`, `Upload-Length` (required) and
`Upload-Metadata` with the upload options as base64 values
(`filename`, `title`, `expires`, `visibility`, `lang`, `kind`,
`keep_meta`, `slug`, `burn_after_read`, `source`, `source_url`, `meta`). The
`Location` header is the upload URL. Limit: `max_resumable_bytes`
(default 2 GiB). Uploads not finished within 24 hours are discarded.
Requires scope `upload`. Protocol errors (400, 413) are tus-style
plain text.

| status | meaning | body |
|---|---|---|
| 201 | Created; see the Location header. | — |
| 400 | Bad Upload-Length or metadata (text/plain). | — |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 413 | Larger than max_resumable_bytes or the token's quota (text/plain). | — |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="patch-api-v1-uploads-id"></a>
### `PATCH /api/v1/uploads/{id}`

**Send bytes (tus)**

Body: the bytes starting at `Upload-Offset`
(`Content-Type: application/offset+octet-stream`). The PATCH that
completes the upload stores the item and returns `Tibi-Item-Id` and
`Tibi-Item-Url` headers.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

| status | meaning | body |
|---|---|---|
| 204 | Accepted; see the new Upload-Offset. | — |
| 400 | The finished upload could not be stored (text/plain). | — |
| 404 | No such upload. | — |
| 409 | Upload-Offset does not match the server's. | — |

<a id="delete-api-v1-uploads-id"></a>
### `DELETE /api/v1/uploads/{id}`

**Abandon an upload (tus)**

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

| status | meaning | body |
|---|---|---|
| 204 | Abandoned. | — |
| 404 | No such upload. | — |

<a id="get-api-v1-uploads-id-item"></a>
### `GET /api/v1/uploads/{id}/item`

**The item a finished upload became**

Requires scope `upload` and the token that created the upload.

| parameter | in | type | description |
|---|---|---|---|
| `id` | path, required | string |  |

| status | meaning | body |
|---|---|---|
| 200 | The item. | `application/json` [Item](#schema-item) |
| 400 | The request is invalid; `message` says why. | `application/json` [Error](#schema-error) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 403 | The token lacks the required scope. | `application/json` [Error](#schema-error) |
| 404 | No such item (or it was deleted or has expired). | `application/json` [Error](#schema-error) |
| 409 | Not finished yet; `message` says how many bytes arrived. | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="get-api-v1-whoami"></a>
### `GET /api/v1/whoami`

**Describe the calling token**

Any valid token may call this; clients use it to verify a login.

| status | meaning | body |
|---|---|---|
| 200 | The token's name, scopes and quota. | `application/json` [Whoami](#schema-whoami) |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |
| 429 | The token made too many requests (`rate_limit` per minute, bursts of `rate_burst`); retry after `Retry-After` seconds. | `application/json` [Error](#schema-error) |

<a id="get-healthz"></a>
### `GET /healthz`

**Liveness probe**

No authentication.

| status | meaning | body |
|---|---|---|
| 200 | The server and its database are up. | `text/plain` string |
| 503 | The database is unavailable. | `text/plain` string |

<a id="get-metrics"></a>
### `GET /metrics`

**Prometheus metrics**

Prometheus text format: requests, upload and served bytes, GC
activity, items by kind, storage used, today's traffic. Allowed with
an `admin` token, or from the same machine on a direct connection
(requests forwarded by a reverse proxy never count as local).

| status | meaning | body |
|---|---|---|
| 200 | Metrics. | `text/plain` string |
| 401 | The token is missing, invalid or revoked. | `application/json` [Error](#schema-error) |

## Schemas

<a id="schema-collection"></a>
### Collection

| field | type | required | description |
|---|---|---|---|
| `created_at` | integer | yes |  |
| `expires_at` | integer or null | yes |  |
| `id` | string | yes |  |
| `item_count` | integer | yes |  |
| `url` | string (uri) | yes | The collection page, `/c/{id}`. |
| `visibility` | string | yes | One of: `unlisted`, `private`. |
| `items` | array of [Item](#schema-item) |  | Present when a single collection is returned. |
| `title` | string |  |  |

<a id="schema-collectionpage"></a>
### CollectionPage

| field | type | required | description |
|---|---|---|---|
| `collections` | array of [Collection](#schema-collection) | yes |  |
| `next_cursor` | string or null | yes |  |

<a id="schema-collectionupload"></a>
### CollectionUpload

| field | type | required | description |
|---|---|---|---|
| `collection` | [Collection](#schema-collection) | yes |  |
| `items` | array of [Item](#schema-item) | yes | The items just uploaded. |

<a id="schema-error"></a>
### Error

| field | type | required | description |
|---|---|---|---|
| `error` | object | yes |  |

<a id="schema-item"></a>
### Item

| field | type | required | description |
|---|---|---|---|
| `burn_after_read` | boolean | yes |  |
| `created_at` | integer | yes | Unix seconds. |
| `download_url` | string (uri) | yes |  |
| `expires_at` | integer or null | yes | Unix seconds; null for never. |
| `id` | string | yes |  |
| `kind` | [Kind](#schema-kind) | yes |  |
| `markdown` | string | yes | `![](raw_url)` for images, `[name](url)` otherwise. |
| `mime` | string | yes | Type of the stored bytes. |
| `raw_url` | string (uri) | yes | Raw bytes, `/{id}.{ext}`: the URL to embed. |
| `size` | integer | yes | Stored bytes (after metadata removal or conversion). |
| `url` | string (uri) | yes | View page. |
| `view_count` | integer | yes |  |
| `visibility` | string | yes | One of: `unlisted`, `private`. |
| `charset` | string |  | Text: original charset when converted to UTF-8, e.g. `GBK`. |
| `collection_id` | string |  |  |
| `collection_url` | string (uri) |  |  |
| `duration_ms` | integer |  | Video and audio length |
| `excerpt` | string |  | Text items, in the item list only: the first 300 characters (none for burn-after-read). |
| `filename` | string |  |  |
| `forked_from` | string |  | ID of the item this one is a new version of. |
| `height` | integer |  | Images: display height. |
| `lang` | string |  | Text: language, e.g. `go`, `python`, `markdown`, `text`. |
| `orig_mime` | string |  | Original type when the upload was converted (e.g. `image/heic`). |
| `position` | integer |  | Order within the collection. |
| `slug` | string |  |  |
| `slug_url` | string (uri) |  |  |
| `source` | string |  |  |
| `source_url` | string (uri) |  | Where it came from (see the upload option). Only for the token that uploaded it and `read_private` tokens. |
| `thumbnail_url` | string (uri) |  | Images: 400px-wide WebP thumbnail, made on first request (404 when none can be made, e.g. a decompression bomb). |
| `title` | string |  |  |
| `width` | integer |  | Images: display width (EXIF rotation applied). |

<a id="schema-itempage"></a>
### ItemPage

| field | type | required | description |
|---|---|---|---|
| `items` | array of [Item](#schema-item) | yes |  |
| `next_cursor` | string or null | yes | Pass as `cursor` for the next page; null on the last page. |

<a id="schema-kind"></a>
### Kind

One of: `image`, `video`, `audio`, `text`, `file`, `encrypted`.

<a id="schema-searchhit"></a>
### SearchHit

An Item plus `snippet`, the line of text containing the first query word.

| field | type | required | description |
|---|---|---|---|
| `burn_after_read` | boolean | yes |  |
| `created_at` | integer | yes | Unix seconds. |
| `download_url` | string (uri) | yes |  |
| `expires_at` | integer or null | yes | Unix seconds; null for never. |
| `id` | string | yes |  |
| `kind` | [Kind](#schema-kind) | yes |  |
| `markdown` | string | yes | `![](raw_url)` for images, `[name](url)` otherwise. |
| `mime` | string | yes | Type of the stored bytes. |
| `raw_url` | string (uri) | yes | Raw bytes, `/{id}.{ext}`: the URL to embed. |
| `size` | integer | yes | Stored bytes (after metadata removal or conversion). |
| `url` | string (uri) | yes | View page. |
| `view_count` | integer | yes |  |
| `visibility` | string | yes | One of: `unlisted`, `private`. |
| `charset` | string |  | Text: original charset when converted to UTF-8, e.g. `GBK`. |
| `collection_id` | string |  |  |
| `collection_url` | string (uri) |  |  |
| `duration_ms` | integer |  | Video and audio length |
| `excerpt` | string |  | Text items, in the item list only: the first 300 characters (none for burn-after-read). |
| `filename` | string |  |  |
| `forked_from` | string |  | ID of the item this one is a new version of. |
| `height` | integer |  | Images: display height. |
| `lang` | string |  | Text: language, e.g. `go`, `python`, `markdown`, `text`. |
| `orig_mime` | string |  | Original type when the upload was converted (e.g. `image/heic`). |
| `position` | integer |  | Order within the collection. |
| `slug` | string |  |  |
| `slug_url` | string (uri) |  |  |
| `snippet` | string |  |  |
| `source` | string |  |  |
| `source_url` | string (uri) |  | Where it came from (see the upload option). Only for the token that uploaded it and `read_private` tokens. |
| `thumbnail_url` | string (uri) |  | Images: 400px-wide WebP thumbnail, made on first request (404 when none can be made, e.g. a decompression bomb). |
| `title` | string |  |  |
| `width` | integer |  | Images: display width (EXIF rotation applied). |

<a id="schema-searchpage"></a>
### SearchPage

| field | type | required | description |
|---|---|---|---|
| `items` | array of [SearchHit](#schema-searchhit) | yes |  |
| `next_cursor` | string or null | yes |  |

<a id="schema-sharelink"></a>
### ShareLink

| field | type | required | description |
|---|---|---|---|
| `expires_at` | integer | yes | Unix seconds. |
| `url` | string (uri) | yes |  |

<a id="schema-stats"></a>
### Stats

| field | type | required | description |
|---|---|---|---|
| `bytes_served_today` | integer | yes | Since midnight UTC; what daily caps count. |
| `bytes_served_total` | integer | yes |  |
| `collections` | integer | yes |  |
| `items` | integer | yes | Live items. |
| `items_by_kind` | object | yes | Live items per kind, e.g. `{"image": 12}`. |
| `logical_bytes` | integer | yes | Total size of live items. |
| `storage_bytes` | integer | yes | Bytes of distinct stored blobs (identical content is stored once). |
| `tokens` | integer | yes | Active (not revoked) tokens. |
| `top_items` | array of object | yes |  |

<a id="schema-whoami"></a>
### Whoami

| field | type | required | description |
|---|---|---|---|
| `name` | string | yes |  |
| `quota_bytes` | integer or null | yes | Upload quota in bytes; null for unlimited. |
| `scopes` | array of string | yes |  |
| `used_bytes` | integer | yes | Total size of the live items this token uploaded (what the quota counts). |


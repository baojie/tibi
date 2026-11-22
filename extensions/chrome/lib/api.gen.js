// Generated from api/openapi.yaml (tibi API 0.1.3) by `go run ./tools/apidoc -js`. Do not edit.

/**
 * @typedef {Object} Collection
 * @property {number} created_at
 * @property {number|null} expires_at
 * @property {string} id
 * @property {number} item_count
 * @property {Array<Item>} [items] - Present when a single collection is returned.
 * @property {string} [title]
 * @property {string} url - The collection page, `/c/{id}`.
 * @property {"unlisted"|"private"} visibility
 */

/**
 * @typedef {Object} CollectionPage
 * @property {Array<Collection>} collections
 * @property {string|null} next_cursor
 */

/**
 * @typedef {Object} CollectionUpload
 * @property {Collection} collection
 * @property {Array<Item>} items - The items just uploaded.
 */

/**
 * @typedef {Object} Error
 * @property {Object<string, *>} error
 */

/**
 * @typedef {Object} Item
 * @property {boolean} burn_after_read
 * @property {string} [charset] - Text: original charset when converted to UTF-8, e.g. `GBK`.
 * @property {string} [collection_id]
 * @property {string} [collection_url]
 * @property {number} created_at - Unix seconds.
 * @property {string} download_url
 * @property {number} [duration_ms] - Video and audio length
 * @property {string} [excerpt] - Text items, in the item list only: the first 300 characters (none for burn-after-read).
 * @property {number|null} expires_at - Unix seconds; null for never.
 * @property {string} [filename]
 * @property {string} [forked_from] - ID of the item this one is a new version of.
 * @property {number} [height] - Images: display height.
 * @property {string} id
 * @property {Kind} kind
 * @property {string} [lang] - Text: language, e.g. `go`, `python`, `markdown`, `text`.
 * @property {string} markdown - `![](raw_url)` for images, `[name](url)` otherwise.
 * @property {string} mime - Type of the stored bytes.
 * @property {string} [orig_mime] - Original type when the upload was converted (e.g. `image/heic`).
 * @property {number} [position] - Order within the collection.
 * @property {string} raw_url - Raw bytes, `/{id}.{ext}`: the URL to embed.
 * @property {number} size - Stored bytes (after metadata removal or conversion).
 * @property {string} [slug]
 * @property {string} [slug_url]
 * @property {string} [source]
 * @property {string} [thumbnail_url] - Images: 400px-wide WebP thumbnail, made on first request (404 when none can be made, e.g. a decompression bomb).
 * @property {string} [title]
 * @property {string} url - View page.
 * @property {number} view_count
 * @property {"unlisted"|"private"} visibility
 * @property {number} [width] - Images: display width (EXIF rotation applied).
 */

/**
 * @typedef {Object} ItemPage
 * @property {Array<Item>} items
 * @property {string|null} next_cursor - Pass as `cursor` for the next page; null on the last page.
 */

/** @typedef {"image"|"video"|"audio"|"text"|"file"|"encrypted"} Kind */

/**
 * @typedef {Object} SearchHit
 * @property {boolean} burn_after_read
 * @property {string} [charset] - Text: original charset when converted to UTF-8, e.g. `GBK`.
 * @property {string} [collection_id]
 * @property {string} [collection_url]
 * @property {number} created_at - Unix seconds.
 * @property {string} download_url
 * @property {number} [duration_ms] - Video and audio length
 * @property {string} [excerpt] - Text items, in the item list only: the first 300 characters (none for burn-after-read).
 * @property {number|null} expires_at - Unix seconds; null for never.
 * @property {string} [filename]
 * @property {string} [forked_from] - ID of the item this one is a new version of.
 * @property {number} [height] - Images: display height.
 * @property {string} id
 * @property {Kind} kind
 * @property {string} [lang] - Text: language, e.g. `go`, `python`, `markdown`, `text`.
 * @property {string} markdown - `![](raw_url)` for images, `[name](url)` otherwise.
 * @property {string} mime - Type of the stored bytes.
 * @property {string} [orig_mime] - Original type when the upload was converted (e.g. `image/heic`).
 * @property {number} [position] - Order within the collection.
 * @property {string} raw_url - Raw bytes, `/{id}.{ext}`: the URL to embed.
 * @property {number} size - Stored bytes (after metadata removal or conversion).
 * @property {string} [slug]
 * @property {string} [slug_url]
 * @property {string} [snippet]
 * @property {string} [source]
 * @property {string} [thumbnail_url] - Images: 400px-wide WebP thumbnail, made on first request (404 when none can be made, e.g. a decompression bomb).
 * @property {string} [title]
 * @property {string} url - View page.
 * @property {number} view_count
 * @property {"unlisted"|"private"} visibility
 * @property {number} [width] - Images: display width (EXIF rotation applied).
 */

/**
 * @typedef {Object} SearchPage
 * @property {Array<SearchHit>} items
 * @property {string|null} next_cursor
 */

/**
 * @typedef {Object} ShareLink
 * @property {number} expires_at - Unix seconds.
 * @property {string} url
 */

/**
 * @typedef {Object} Stats
 * @property {number} bytes_served_today - Since midnight UTC; what daily caps count.
 * @property {number} bytes_served_total
 * @property {number} collections
 * @property {number} items - Live items.
 * @property {Object<string, *>} items_by_kind - Live items per kind, e.g. `{"image": 12}`.
 * @property {number} logical_bytes - Total size of live items.
 * @property {number} storage_bytes - Bytes of distinct stored blobs (identical content is stored once).
 * @property {number} tokens - Active (not revoked) tokens.
 * @property {Array<Object<string, *>>} top_items
 */

/**
 * @typedef {Object} Whoami
 * @property {string} name
 * @property {number|null} quota_bytes - Upload quota in bytes; null for unlimited.
 * @property {Array<"upload"|"read_private"|"delete"|"admin">} scopes
 * @property {number} used_bytes - Total size of the live items this token uploaded (what the quota counts).
 */

/** Operations: method, path (with {params}) and required scope. */
export const ENDPOINTS = Object.freeze({
  listCollections: { method: "GET", path: "/api/v1/collections", scope: "upload" },
  createCollection: { method: "POST", path: "/api/v1/collections", scope: "upload" },
  getCollection: { method: "GET", path: "/api/v1/collections/{id}", scope: "upload" },
  patchCollection: { method: "PATCH", path: "/api/v1/collections/{id}", scope: "upload" },
  deleteCollection: { method: "DELETE", path: "/api/v1/collections/{id}", scope: "delete" },
  listItems: { method: "GET", path: "/api/v1/items", scope: "upload" },
  upload: { method: "POST", path: "/api/v1/items", scope: "upload" },
  getItem: { method: "GET", path: "/api/v1/items/{id}", scope: "upload" },
  patchItem: { method: "PATCH", path: "/api/v1/items/{id}", scope: "upload" },
  deleteItem: { method: "DELETE", path: "/api/v1/items/{id}", scope: "delete" },
  forkItem: { method: "POST", path: "/api/v1/items/{id}/fork", scope: "upload" },
  shareLink: { method: "POST", path: "/api/v1/items/{id}/share-link", scope: "upload" },
  openapi: { method: "GET", path: "/api/v1/openapi.yaml", scope: "" },
  search: { method: "GET", path: "/api/v1/search", scope: "" },
  stats: { method: "GET", path: "/api/v1/stats", scope: "admin" },
  tusCreate: { method: "POST", path: "/api/v1/uploads/", scope: "upload" },
  tusPatch: { method: "PATCH", path: "/api/v1/uploads/{id}", scope: "" },
  tusHead: { method: "HEAD", path: "/api/v1/uploads/{id}", scope: "" },
  tusDelete: { method: "DELETE", path: "/api/v1/uploads/{id}", scope: "" },
  uploadItem: { method: "GET", path: "/api/v1/uploads/{id}/item", scope: "upload" },
  whoami: { method: "GET", path: "/api/v1/whoami", scope: "" },
  healthz: { method: "GET", path: "/healthz", scope: "" },
  metrics: { method: "GET", path: "/metrics", scope: "" },
});

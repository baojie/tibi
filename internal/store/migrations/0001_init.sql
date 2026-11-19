CREATE TABLE tokens (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    hash         TEXT NOT NULL UNIQUE,
    scopes       TEXT NOT NULL,
    quota_bytes  INTEGER,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER
);

CREATE TABLE items (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL,
    mime            TEXT NOT NULL,
    orig_mime       TEXT,
    filename        TEXT,
    ext             TEXT NOT NULL,
    size            INTEGER NOT NULL,
    sha256          TEXT NOT NULL,
    title           TEXT,
    lang            TEXT,
    charset         TEXT,
    width           INTEGER,
    height          INTEGER,
    duration_ms     INTEGER,
    visibility      TEXT NOT NULL DEFAULT 'unlisted',
    slug            TEXT UNIQUE,
    collection_id   TEXT,
    position        INTEGER,
    forked_from     TEXT,
    burn_after_read INTEGER NOT NULL DEFAULT 0,
    source          TEXT,
    meta            TEXT,
    token_id        INTEGER NOT NULL REFERENCES tokens(id),
    view_count      INTEGER NOT NULL DEFAULT 0,
    bytes_served    INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    expires_at      INTEGER,
    deleted_at      INTEGER
);

CREATE INDEX items_created_at ON items(created_at);
CREATE INDEX items_sha256 ON items(sha256);
CREATE INDEX items_expires_at ON items(expires_at);
CREATE INDEX items_collection_id ON items(collection_id);
CREATE INDEX items_source ON items(source);

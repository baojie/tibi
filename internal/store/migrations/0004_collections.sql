-- Albums and multi-file pastes (PLAN A4). IDs share the item namespace.
CREATE TABLE collections (
    id          TEXT PRIMARY KEY,
    title       TEXT,
    visibility  TEXT NOT NULL DEFAULT 'unlisted',
    slug        TEXT UNIQUE,
    token_id    INTEGER NOT NULL REFERENCES tokens(id),
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER,
    deleted_at  INTEGER
);
CREATE INDEX collections_created_at ON collections(created_at);
CREATE INDEX items_forked_from ON items(forked_from);

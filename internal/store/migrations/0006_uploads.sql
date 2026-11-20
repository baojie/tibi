-- Resumable (tus) uploads in progress or recently finished (PLAN A4).
CREATE TABLE uploads (
    id          TEXT PRIMARY KEY,        -- tus upload ID
    token_id    INTEGER NOT NULL REFERENCES tokens(id),
    options     TEXT NOT NULL,           -- JSON: upload options from Upload-Metadata
    size        INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    item_id     TEXT,                    -- set when finished
    error       TEXT                     -- set when finishing failed
);
CREATE INDEX uploads_created_at ON uploads(created_at);

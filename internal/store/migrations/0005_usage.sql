-- Bytes served per UTC day, per item; item_id '' holds the instance total.
-- Used for daily bandwidth caps (PLAN A8.10) and stats.
CREATE TABLE daily_usage (
    day     TEXT NOT NULL,      -- YYYY-MM-DD (UTC)
    item_id TEXT NOT NULL,
    bytes   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, item_id)
);
CREATE INDEX items_token_id ON items(token_id);

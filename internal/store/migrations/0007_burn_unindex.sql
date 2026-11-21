-- Burn-after-read content must not be searchable (design 2026-11-21 §5):
-- drop the search rows of such items; the startup backfill re-indexes
-- their titles and file names only.
DELETE FROM items_fts WHERE rowid IN (SELECT rowid FROM items WHERE burn_after_read = 1);
DELETE FROM items_tri WHERE rowid IN (SELECT rowid FROM items WHERE burn_after_read = 1);

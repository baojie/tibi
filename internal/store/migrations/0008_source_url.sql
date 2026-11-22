-- Where an item came from (an image's address, the page a text or
-- screenshot was taken from). Shown only to its uploader and read_private
-- tokens. Chrome uploads so far kept the page in meta.page_url.
ALTER TABLE items ADD COLUMN source_url TEXT;
UPDATE items SET source_url = CASE WHEN json_valid(meta) THEN json_extract(meta, '$.page_url') END
  WHERE meta IS NOT NULL AND source_url IS NULL;
UPDATE items SET source_url = NULL
  WHERE source_url IS NOT NULL AND (typeof(source_url) <> 'text' OR (source_url NOT LIKE 'http://%' AND source_url NOT LIKE 'https://%'));

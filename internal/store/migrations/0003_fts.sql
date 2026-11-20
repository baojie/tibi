-- Full-text search (PLAN A4 items_fts). rowid = items.rowid.
-- Word search; cjk holds CJK text split into overlapping bigrams so that
-- 1- and 2-character Chinese/Japanese/Korean queries match.
CREATE VIRTUAL TABLE items_fts USING fts5(title, filename, body, cjk, tokenize = 'unicode61 remove_diacritics 2');
-- Substring search (code identifiers, ≥3 characters).
CREATE VIRTUAL TABLE items_tri USING fts5(title, filename, body, tokenize = 'trigram');

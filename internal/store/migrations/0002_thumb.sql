-- 1 when a WebP thumbnail exists at thumb/ab/cd/<sha256>-<ThumbWidth>.webp
ALTER TABLE items ADD COLUMN thumb INTEGER NOT NULL DEFAULT 0;

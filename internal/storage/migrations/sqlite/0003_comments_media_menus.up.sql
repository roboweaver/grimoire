-- M4: comments, media library, and navigation-menu support (SQLite).
--
-- Migration contract: this runs only against grimoire-provisioned schemas.
-- A pre-existing live WordPress database already has these tables and columns
-- and is read as-is.

CREATE TABLE IF NOT EXISTS {{prefix}}comments (
  "comment_ID" INTEGER PRIMARY KEY AUTOINCREMENT,
  comment_post_ID INTEGER NOT NULL DEFAULT 0,
  comment_author TEXT NOT NULL DEFAULT '',
  comment_author_email TEXT NOT NULL DEFAULT '',
  comment_author_url TEXT NOT NULL DEFAULT '',
  comment_author_IP TEXT NOT NULL DEFAULT '',
  comment_date TEXT NOT NULL DEFAULT '1970-01-01 00:00:00',
  comment_date_gmt TEXT NOT NULL DEFAULT '1970-01-01 00:00:00',
  comment_content TEXT NOT NULL DEFAULT '',
  comment_approved TEXT NOT NULL DEFAULT '1',
  comment_agent TEXT NOT NULL DEFAULT '',
  comment_parent INTEGER NOT NULL DEFAULT 0,
  user_id INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS {{prefix}}comments_post_id ON {{prefix}}comments (comment_post_ID);
CREATE INDEX IF NOT EXISTS {{prefix}}comments_approved_date_gmt ON {{prefix}}comments (comment_approved, comment_date_gmt);
CREATE INDEX IF NOT EXISTS {{prefix}}comments_date_gmt ON {{prefix}}comments (comment_date_gmt);
CREATE INDEX IF NOT EXISTS {{prefix}}comments_parent ON {{prefix}}comments (comment_parent);
CREATE INDEX IF NOT EXISTS {{prefix}}comments_author_email ON {{prefix}}comments (comment_author_email);
CREATE INDEX IF NOT EXISTS {{prefix}}comments_user_id ON {{prefix}}comments (user_id);

CREATE TABLE IF NOT EXISTS {{prefix}}commentmeta (
  meta_id INTEGER PRIMARY KEY AUTOINCREMENT,
  comment_id INTEGER NOT NULL DEFAULT 0,
  meta_key TEXT DEFAULT NULL,
  meta_value TEXT
);
CREATE INDEX IF NOT EXISTS {{prefix}}commentmeta_comment_id ON {{prefix}}commentmeta (comment_id);
CREATE INDEX IF NOT EXISTS {{prefix}}commentmeta_meta_key ON {{prefix}}commentmeta (meta_key);

ALTER TABLE {{prefix}}posts ADD COLUMN comment_status TEXT NOT NULL DEFAULT 'open';
ALTER TABLE {{prefix}}posts ADD COLUMN post_parent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE {{prefix}}posts ADD COLUMN post_mime_type TEXT NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}posts ADD COLUMN menu_order INTEGER NOT NULL DEFAULT 0;

-- WordPress-compatible schema (SQLite). Types translated: integer PKs ->
-- INTEGER PRIMARY KEY AUTOINCREMENT; all text -> TEXT; DATETIME -> TEXT
-- (ISO-8601); UNSIGNED/engine/charset dropped.

CREATE TABLE IF NOT EXISTS {{prefix}}posts (
  "ID" INTEGER PRIMARY KEY AUTOINCREMENT,
  post_author INTEGER NOT NULL DEFAULT 0,
  post_date TEXT NOT NULL DEFAULT '1970-01-01 00:00:00',
  post_content TEXT NOT NULL DEFAULT '',
  post_title TEXT NOT NULL DEFAULT '',
  post_excerpt TEXT NOT NULL DEFAULT '',
  post_status TEXT NOT NULL DEFAULT 'publish',
  post_name TEXT NOT NULL DEFAULT '',
  post_type TEXT NOT NULL DEFAULT 'post'
);
CREATE INDEX IF NOT EXISTS {{prefix}}posts_type_status_date ON {{prefix}}posts (post_type, post_status, post_date, "ID");
CREATE INDEX IF NOT EXISTS {{prefix}}posts_post_name ON {{prefix}}posts (post_name);

CREATE TABLE IF NOT EXISTS {{prefix}}postmeta (
  meta_id INTEGER PRIMARY KEY AUTOINCREMENT,
  post_id INTEGER NOT NULL DEFAULT 0,
  meta_key TEXT DEFAULT NULL,
  meta_value TEXT
);
CREATE INDEX IF NOT EXISTS {{prefix}}postmeta_post_id ON {{prefix}}postmeta (post_id);
CREATE INDEX IF NOT EXISTS {{prefix}}postmeta_meta_key ON {{prefix}}postmeta (meta_key);

CREATE TABLE IF NOT EXISTS {{prefix}}options (
  option_id INTEGER PRIMARY KEY AUTOINCREMENT,
  option_name TEXT NOT NULL DEFAULT '',
  option_value TEXT NOT NULL DEFAULT '',
  autoload TEXT NOT NULL DEFAULT 'yes',
  UNIQUE (option_name)
);

CREATE TABLE IF NOT EXISTS {{prefix}}terms (
  term_id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL DEFAULT '',
  slug TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS {{prefix}}terms_slug ON {{prefix}}terms (slug);

CREATE TABLE IF NOT EXISTS {{prefix}}term_taxonomy (
  term_taxonomy_id INTEGER PRIMARY KEY AUTOINCREMENT,
  term_id INTEGER NOT NULL DEFAULT 0,
  taxonomy TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  parent INTEGER NOT NULL DEFAULT 0,
  count INTEGER NOT NULL DEFAULT 0,
  UNIQUE (term_id, taxonomy)
);
CREATE INDEX IF NOT EXISTS {{prefix}}term_taxonomy_taxonomy ON {{prefix}}term_taxonomy (taxonomy);

CREATE TABLE IF NOT EXISTS {{prefix}}term_relationships (
  object_id INTEGER NOT NULL DEFAULT 0,
  term_taxonomy_id INTEGER NOT NULL DEFAULT 0,
  term_order INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (object_id, term_taxonomy_id)
);
CREATE INDEX IF NOT EXISTS {{prefix}}term_relationships_ttid ON {{prefix}}term_relationships (term_taxonomy_id);

CREATE TABLE IF NOT EXISTS {{prefix}}users (
  "ID" INTEGER PRIMARY KEY AUTOINCREMENT,
  user_login TEXT NOT NULL DEFAULT '',
  user_nicename TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS {{prefix}}users_user_login ON {{prefix}}users (user_login);

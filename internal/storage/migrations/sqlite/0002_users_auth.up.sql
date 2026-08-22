-- M2: user authentication columns, usermeta, and sessions (SQLite).
-- Extends the minimal {{prefix}}users table created in 0001 and adds the
-- WordPress-compatible usermeta table plus grimoire's native sessions table.
--
-- Migration contract: this runs only against grimoire-provisioned schemas
-- (a fresh 0001 users table with four columns). A pre-existing full WordPress
-- database already has these columns and the usermeta table, so it is read
-- as-is and this migration is not pointed at it; only {{prefix}}sessions is
-- grimoire-native and is created with IF NOT EXISTS.

ALTER TABLE {{prefix}}users ADD COLUMN user_pass TEXT NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}users ADD COLUMN user_email TEXT NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}users ADD COLUMN user_url TEXT NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}users ADD COLUMN user_registered TEXT NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}users ADD COLUMN user_activation_key TEXT NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}users ADD COLUMN user_status INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS {{prefix}}usermeta (
  umeta_id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL DEFAULT 0,
  meta_key TEXT DEFAULT NULL,
  meta_value TEXT
);
CREATE INDEX IF NOT EXISTS {{prefix}}usermeta_user_id ON {{prefix}}usermeta (user_id);
CREATE INDEX IF NOT EXISTS {{prefix}}usermeta_meta_key ON {{prefix}}usermeta (meta_key);

CREATE TABLE IF NOT EXISTS {{prefix}}sessions (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL DEFAULT 0,
  csrf_token TEXT NOT NULL DEFAULT '',
  created TEXT NOT NULL DEFAULT '1970-01-01 00:00:00',
  expires TEXT NOT NULL DEFAULT '1970-01-01 00:00:00'
);
CREATE INDEX IF NOT EXISTS {{prefix}}sessions_expires ON {{prefix}}sessions (expires);
CREATE INDEX IF NOT EXISTS {{prefix}}sessions_user_id ON {{prefix}}sessions (user_id);

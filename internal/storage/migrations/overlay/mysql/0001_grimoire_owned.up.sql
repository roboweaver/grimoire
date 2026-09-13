-- Overlay migration set (MySQL / MariaDB): grimoire-owned tables ONLY.
--
-- This set is what `grimoire-cli migrate -overlay` applies. Unlike the
-- greenfield set in ../../mysql, it is safe to run against a pre-existing,
-- populated WordPress database:
--
--   * It creates ONLY tables that WordPress itself never creates, so it can
--     never collide with a stock WP schema.
--   * Every statement is guarded (CREATE TABLE IF NOT EXISTS), so re-running it
--     is a no-op.
--   * It contains NO ALTER TABLE. It never adds, drops, retypes, or reorders a
--     column on a table WordPress owns, and it never touches existing rows.
--
-- {{prefix}}sessions is grimoire's native session store (WordPress keeps login
-- sessions in usermeta; grimoire uses a real table). The DDL below is kept
-- identical to the greenfield 0002_users_auth.up.sql definition -- the
-- migration contract suite asserts both paths produce the same column set, so
-- the two cannot drift.

CREATE TABLE IF NOT EXISTS {{prefix}}sessions (
  id VARCHAR(64) NOT NULL,
  user_id BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  csrf_token VARCHAR(64) NOT NULL DEFAULT '',
  created DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00',
  expires DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00',
  PRIMARY KEY (id),
  KEY expires (expires),
  KEY user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

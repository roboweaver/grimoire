-- M2: user authentication columns, usermeta, and sessions (MySQL / MariaDB).
-- Extends the minimal {{prefix}}users table created in 0001 and adds the
-- WordPress-compatible usermeta table plus grimoire's native sessions table.
--
-- Migration contract: this runs only against grimoire-provisioned schemas
-- (a fresh 0001 users table with four columns). MySQL lacks ADD COLUMN
-- IF NOT EXISTS, so pointing this at a pre-existing full WordPress database is
-- unsupported; that database already has the columns and usermeta table and is
-- read as-is. Only {{prefix}}sessions is grimoire-native (IF NOT EXISTS).

ALTER TABLE {{prefix}}users ADD COLUMN user_pass VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}users ADD COLUMN user_email VARCHAR(100) NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}users ADD COLUMN user_url VARCHAR(100) NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}users ADD COLUMN user_registered DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}users ADD COLUMN user_activation_key VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}users ADD COLUMN user_status INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS {{prefix}}usermeta (
  umeta_id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  meta_key VARCHAR(255) DEFAULT NULL,
  meta_value LONGTEXT,
  PRIMARY KEY (umeta_id),
  KEY user_id (user_id),
  KEY meta_key (meta_key(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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

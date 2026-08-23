-- M4: comments, media library, and navigation-menu support (MySQL / MariaDB).
--
-- Migration contract: this runs only against grimoire-provisioned schemas.
-- MySQL lacks ADD COLUMN IF NOT EXISTS, so pointing this at a pre-existing live
-- WordPress database is unsupported; that database already has these tables and
-- columns and is read as-is.

CREATE TABLE IF NOT EXISTS {{prefix}}comments (
  comment_ID BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  comment_post_ID BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  comment_author TINYTEXT NOT NULL,
  comment_author_email VARCHAR(100) NOT NULL DEFAULT '',
  comment_author_url VARCHAR(200) NOT NULL DEFAULT '',
  comment_author_IP VARCHAR(100) NOT NULL DEFAULT '',
  comment_date DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00',
  comment_date_gmt DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00',
  comment_content TEXT NOT NULL,
  comment_approved VARCHAR(20) NOT NULL DEFAULT '1',
  comment_agent VARCHAR(255) NOT NULL DEFAULT '',
  comment_parent BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  user_id BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  PRIMARY KEY (comment_ID),
  KEY comment_post_ID (comment_post_ID),
  KEY comment_approved_date_gmt (comment_approved, comment_date_gmt),
  KEY comment_date_gmt (comment_date_gmt),
  KEY comment_parent (comment_parent),
  KEY comment_author_email (comment_author_email(191)),
  KEY user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS {{prefix}}commentmeta (
  meta_id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  comment_id BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  meta_key VARCHAR(255) DEFAULT NULL,
  meta_value LONGTEXT,
  PRIMARY KEY (meta_id),
  KEY comment_id (comment_id),
  KEY meta_key (meta_key(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE {{prefix}}posts ADD COLUMN comment_status VARCHAR(20) NOT NULL DEFAULT 'open';
ALTER TABLE {{prefix}}posts ADD COLUMN post_parent BIGINT(20) UNSIGNED NOT NULL DEFAULT 0;
ALTER TABLE {{prefix}}posts ADD COLUMN post_mime_type VARCHAR(100) NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}posts ADD COLUMN menu_order INT NOT NULL DEFAULT 0;

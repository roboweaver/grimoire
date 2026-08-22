-- WordPress-compatible schema (MySQL / MariaDB). All tables carry the
-- configured table prefix (default wp_) via the {{prefix}} token.

CREATE TABLE IF NOT EXISTS {{prefix}}posts (
  ID BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  post_author BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  post_date DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00',
  post_content LONGTEXT NOT NULL,
  post_title TEXT NOT NULL,
  post_excerpt TEXT NOT NULL,
  post_status VARCHAR(20) NOT NULL DEFAULT 'publish',
  post_name VARCHAR(200) NOT NULL DEFAULT '',
  post_type VARCHAR(20) NOT NULL DEFAULT 'post',
  PRIMARY KEY (ID),
  KEY type_status_date (post_type, post_status, post_date, ID),
  KEY post_name (post_name(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS {{prefix}}postmeta (
  meta_id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  post_id BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  meta_key VARCHAR(255) DEFAULT NULL,
  meta_value LONGTEXT,
  PRIMARY KEY (meta_id),
  KEY post_id (post_id),
  KEY meta_key (meta_key(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS {{prefix}}options (
  option_id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  option_name VARCHAR(191) NOT NULL DEFAULT '',
  option_value LONGTEXT NOT NULL,
  autoload VARCHAR(20) NOT NULL DEFAULT 'yes',
  PRIMARY KEY (option_id),
  UNIQUE KEY option_name (option_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS {{prefix}}terms (
  term_id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  name VARCHAR(200) NOT NULL DEFAULT '',
  slug VARCHAR(200) NOT NULL DEFAULT '',
  PRIMARY KEY (term_id),
  KEY slug (slug(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS {{prefix}}term_taxonomy (
  term_taxonomy_id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  term_id BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  taxonomy VARCHAR(32) NOT NULL DEFAULT '',
  description LONGTEXT NOT NULL,
  parent BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  count BIGINT(20) NOT NULL DEFAULT 0,
  PRIMARY KEY (term_taxonomy_id),
  UNIQUE KEY term_id_taxonomy (term_id, taxonomy),
  KEY taxonomy (taxonomy)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS {{prefix}}term_relationships (
  object_id BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  term_taxonomy_id BIGINT(20) UNSIGNED NOT NULL DEFAULT 0,
  term_order INT NOT NULL DEFAULT 0,
  PRIMARY KEY (object_id, term_taxonomy_id),
  KEY term_taxonomy_id (term_taxonomy_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS {{prefix}}users (
  ID BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
  user_login VARCHAR(60) NOT NULL DEFAULT '',
  user_nicename VARCHAR(50) NOT NULL DEFAULT '',
  display_name VARCHAR(250) NOT NULL DEFAULT '',
  PRIMARY KEY (ID),
  KEY user_login_key (user_login)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

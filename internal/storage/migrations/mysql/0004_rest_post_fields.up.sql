-- M5: WordPress REST-parity post columns (MySQL / MariaDB).
--
-- Migration contract: this runs only against grimoire-provisioned schemas.
-- MySQL lacks ADD COLUMN IF NOT EXISTS, so pointing this at a pre-existing
-- live WordPress database is unsupported; that database already has these
-- columns and is read as-is. These columns back the REST
-- `date_gmt`/`modified`/`modified_gmt`/`ping_status`/`password_protected`/
-- `guid.rendered` fields required by Req 2.2, which a greenfield grimoire
-- schema (0001) never populated because nothing before M5 read them.

ALTER TABLE {{prefix}}posts ADD COLUMN post_date_gmt DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN post_modified DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN post_modified_gmt DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN ping_status VARCHAR(20) NOT NULL DEFAULT 'open';
ALTER TABLE {{prefix}}posts ADD COLUMN post_password VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}posts ADD COLUMN guid VARCHAR(255) NOT NULL DEFAULT '';

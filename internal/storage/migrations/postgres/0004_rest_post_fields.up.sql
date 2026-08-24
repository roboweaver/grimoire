-- M5: WordPress REST-parity post columns (PostgreSQL).
--
-- Migration contract: this runs only against grimoire-provisioned schemas
-- (Postgres's ADD COLUMN IF NOT EXISTS makes this safely re-runnable against
-- a pre-existing live WordPress database too, which already has these
-- columns and is read as-is). These columns back the REST
-- `date_gmt`/`modified`/`modified_gmt`/`ping_status`/`password_protected`/
-- `guid.rendered` fields required by Req 2.2, which a greenfield grimoire
-- schema (0001) never populated because nothing before M5 read them.

ALTER TABLE {{prefix}}posts ADD COLUMN IF NOT EXISTS post_date_gmt TIMESTAMP NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN IF NOT EXISTS post_modified TIMESTAMP NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN IF NOT EXISTS post_modified_gmt TIMESTAMP NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN IF NOT EXISTS ping_status VARCHAR(20) NOT NULL DEFAULT 'open';
ALTER TABLE {{prefix}}posts ADD COLUMN IF NOT EXISTS post_password VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}posts ADD COLUMN IF NOT EXISTS guid VARCHAR(255) NOT NULL DEFAULT '';

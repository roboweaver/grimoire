-- M5: WordPress REST-parity post columns (SQLite).
--
-- Migration contract: this runs only against grimoire-provisioned schemas.
-- A pre-existing live WordPress database already has these columns and is
-- read as-is (see M2/M4's column-migration contract). These columns back the
-- REST `date_gmt`/`modified`/`modified_gmt`/`ping_status`/`password_protected`/
-- `guid.rendered` fields required by Req 2.2, which a greenfield grimoire
-- schema (0001) never populated because nothing before M5 read them.

ALTER TABLE {{prefix}}posts ADD COLUMN post_date_gmt TEXT NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN post_modified TEXT NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN post_modified_gmt TEXT NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE {{prefix}}posts ADD COLUMN ping_status TEXT NOT NULL DEFAULT 'open';
ALTER TABLE {{prefix}}posts ADD COLUMN post_password TEXT NOT NULL DEFAULT '';
ALTER TABLE {{prefix}}posts ADD COLUMN guid TEXT NOT NULL DEFAULT '';

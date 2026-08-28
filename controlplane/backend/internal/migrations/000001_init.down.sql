-- RESTRICT, not CASCADE: CASCADE would drop every table later migrations
-- created, turning `migrate down 1` into total data loss. RESTRICT refuses
-- to reverse until the component migrations above are reversed first.
DROP SCHEMA IF EXISTS controlplane RESTRICT;

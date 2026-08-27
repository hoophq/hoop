-- RESTRICT, not CASCADE.
--
-- CASCADE here would drop every table any later migration created, which
-- turns `migrate down 1` during an incident into total data loss for the
-- control plane. RESTRICT makes this migration refuse to reverse until the
-- component migrations above it have been reversed first, which is the
-- order they were applied in.
DROP SCHEMA IF EXISTS controlplane RESTRICT;

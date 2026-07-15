BEGIN;
SET search_path TO private;

-- Track when a connection's secret envvars were last replaced.
-- Used by the UI to display "Last updated" metadata next to write-only
-- credentials. Stays NULL for connections whose secrets have not been
-- modified since this migration ran.
ALTER TABLE connections ADD COLUMN IF NOT EXISTS secrets_updated_at TIMESTAMPTZ;

COMMIT;

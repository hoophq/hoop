BEGIN;

SET search_path TO private;

-- last_error is the reason the sidecar gave when last_outcome is refused or
-- restart. served_revision_at is when served_revision last changed, so a
-- served document nobody reported applying can be told from one served a
-- moment ago. Both NULL until a handshake writes them.
ALTER TABLE sidecars
    ADD COLUMN IF NOT EXISTS last_error TEXT NULL,
    ADD COLUMN IF NOT EXISTS served_revision_at TIMESTAMPTZ NULL;

COMMIT;

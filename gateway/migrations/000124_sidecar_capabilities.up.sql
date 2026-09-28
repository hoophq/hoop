BEGIN;

SET search_path TO private;

-- capabilities lists the served-document features the sidecar reported on its
-- last handshake. NULL means it never reported, and an empty array means a
-- build too old to send the header.
ALTER TABLE sidecars
    ADD COLUMN IF NOT EXISTS capabilities TEXT[] NULL;

COMMIT;

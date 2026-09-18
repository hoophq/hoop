BEGIN;

SET search_path TO private;

-- What a sidecar reports about itself at each handshake.
--
-- Columns rather than the process-local store these replace
-- (gateway/api/sidecar/state.go): a fleet view is the reason the control
-- plane exists, and one that a deploy blanks -- or that answers differently
-- depending on which replica a sidecar last reached -- cannot report a
-- distribution state honestly. A restart is exactly when an admin looks.
--
-- served_revision is the plane's own name for the document it last answered
-- this sidecar with; applied_revision is what the sidecar says it is running.
-- Equal means converged. They are opaque strings the plane both issues and
-- compares, so nothing parses them.
--
-- last_outcome is the sidecar's verdict on the last document it handled
-- ("applied", "restart", "refused", "unchanged", "retry"). It is the only way
-- to tell a sidecar enforcing the current rules from one that refused them
-- and kept the old ones while still handshaking on time.
--
-- Every column is nullable: a sidecar that has never handshaken, and one too
-- old to report, must both read as unknown rather than as converged.
ALTER TABLE sidecars
    ADD COLUMN IF NOT EXISTS last_seen_at     TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS reported_version VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS served_revision  VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS applied_revision VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS last_outcome     VARCHAR(32) NULL;

COMMIT;

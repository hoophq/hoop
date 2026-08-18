-- Statement-level review keys, for the hoop-inspect human-approval gate.
--
-- statement_hash is the AUTHORIZATION key: a SHA-256 (64 hex chars) over the
-- exact canonical statement text, computed by the relay from the bytes on the
-- wire. It is deliberately not unique. The same statement legitimately
-- accumulates many reviews over time — each filed, approved and consumed on
-- its own merits — which is why the claim query orders and limits rather than
-- assuming one match.
--
-- request_marker is the REQUEST identity, not an authorization key: an
-- agent-supplied correlation handle used only to decide whether an incoming
-- request is a retry of one already filed. It never widens what an approval
-- permits.
--
-- Both are NULL on every review created by any other path, which is every row
-- that exists today.
--
-- idx_reviews_claim serves the claim, which is the one query on the data path:
--
--   UPDATE private.reviews SET status = 'EXECUTED' WHERE id = (
--     SELECT id FROM private.reviews
--      WHERE org_id = $1 AND owner_id = $2 AND connection_id = $3
--        AND statement_hash = $4 AND status = 'APPROVED'
--      ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
--
-- Partial on statement_hash IS NOT NULL: only gated reviews carry one, so
-- every other row in the table is dead weight in this index. status is in the
-- key rather than the predicate because a claim also has to find the rows it
-- must NOT return (an already-EXECUTED one) without a heap fetch.
--
-- Deliberately a plain, blocking CREATE INDEX rather than CONCURRENTLY, for
-- the reason spelled out in 000113: golang-migrate Execs each file as one
-- statement batch inside BEGIN/COMMIT, and a CONCURRENTLY build that fails
-- leaves an INVALID index AND a dirty schema_migrations row, which makes the
-- gateway refuse to start. reviews is small relative to sessions, so the SHARE
-- lock is short and only blocks review writes, which are rare.
BEGIN;
SET search_path TO private;

ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS statement_hash VARCHAR(64),
    ADD COLUMN IF NOT EXISTS request_marker VARCHAR(128);

CREATE INDEX IF NOT EXISTS idx_reviews_claim
    ON reviews (org_id, owner_id, connection_id, statement_hash, status, created_at)
    WHERE statement_hash IS NOT NULL;

-- Serves the find-or-create dedupe: "is there already a PENDING review this
-- sandbox filed for this connection under this marker".
CREATE INDEX IF NOT EXISTS idx_reviews_request_marker
    ON reviews (org_id, owner_id, connection_id, request_marker, status)
    WHERE request_marker IS NOT NULL;

COMMIT;

BEGIN;

SET search_path TO private;

-- gen moves on every write that can change a document served to a sidecar of
-- the org: a rule, a rule binding, a sidecar's stored configuration, or the
-- org license. The handshake compares it with the gen it last composed at
-- (sidecars.served_gen) and skips the compose when both match. Triggers, not
-- the write paths, keep it moving: a write path that forgot to bump it would
-- serve a stale document with nothing to notice.
--
-- Its own table, not a column on orgs: writes that lock the org row first
-- (a protection profile switch) and then a rule row would otherwise deadlock
-- with a rule edit taking the two in the other order.
--
-- bumped_by is the transaction that last moved gen, so a transaction moves it
-- once however many rows it writes.
CREATE TABLE IF NOT EXISTS sidecar_config_gens (
    org_id    UUID PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    gen       BIGINT NOT NULL DEFAULT 0,
    bumped_by BIGINT NULL
);

-- served_gen is the gen the served document was composed at; composed_at
-- bounds how long a skip can last, for the inputs that move with time alone
-- (a license term). Both NULL until a handshake composes.
ALTER TABLE sidecars
    ADD COLUMN IF NOT EXISTS served_gen BIGINT NULL,
    ADD COLUMN IF NOT EXISTS composed_at TIMESTAMPTZ NULL;

-- orgs carries its id as id, every other table as org_id.
CREATE OR REPLACE FUNCTION bump_sidecar_config_gen() RETURNS trigger AS $$
DECLARE
    org UUID;
BEGIN
    IF TG_TABLE_NAME = 'orgs' THEN
        org := NEW.id;
    ELSIF TG_OP = 'DELETE' THEN
        org := OLD.org_id;
    ELSE
        org := NEW.org_id;
    END IF;
    INSERT INTO private.sidecar_config_gens AS g (org_id, gen, bumped_by)
    VALUES (org, 1, txid_current())
    ON CONFLICT (org_id) DO UPDATE SET gen = g.gen + 1, bumped_by = EXCLUDED.bumped_by
    WHERE g.bumped_by IS DISTINCT FROM EXCLUDED.bumped_by;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Constraint triggers, deferred to commit, so the gen row is the LAST lock a
-- transaction takes. Two writers then never hold it while waiting on a row
-- the other one holds.
CREATE CONSTRAINT TRIGGER guardrail_rules_sidecar_config_gen
    AFTER INSERT OR UPDATE OR DELETE ON guardrail_rules
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION bump_sidecar_config_gen();
CREATE CONSTRAINT TRIGGER guardrail_rules_listeners_sidecar_config_gen
    AFTER INSERT OR UPDATE OR DELETE ON guardrail_rules_listeners
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION bump_sidecar_config_gen();
CREATE CONSTRAINT TRIGGER datamasking_rules_sidecar_config_gen
    AFTER INSERT OR UPDATE OR DELETE ON datamasking_rules
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION bump_sidecar_config_gen();
CREATE CONSTRAINT TRIGGER datamasking_rules_listeners_sidecar_config_gen
    AFTER INSERT OR UPDATE OR DELETE ON datamasking_rules_listeners
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION bump_sidecar_config_gen();
CREATE CONSTRAINT TRIGGER ai_session_analyzer_rules_sidecar_config_gen
    AFTER INSERT OR UPDATE OR DELETE ON ai_session_analyzer_rules
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION bump_sidecar_config_gen();
CREATE CONSTRAINT TRIGGER ai_session_analyzer_rules_listeners_sidecar_config_gen
    AFTER INSERT OR UPDATE OR DELETE ON ai_session_analyzer_rules_listeners
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION bump_sidecar_config_gen();

-- UPDATE OF keeps the per-minute handshake UPDATE from firing it at all.
CREATE CONSTRAINT TRIGGER sidecars_sidecar_config_gen
    AFTER UPDATE OF configuration ON sidecars
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.configuration IS DISTINCT FROM NEW.configuration)
    EXECUTE FUNCTION bump_sidecar_config_gen();

-- license_data is JSON, which has no equality operator; text compares it.
CREATE CONSTRAINT TRIGGER orgs_license_sidecar_config_gen
    AFTER UPDATE OF license_data ON orgs
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.license_data::text IS DISTINCT FROM NEW.license_data::text)
    EXECUTE FUNCTION bump_sidecar_config_gen();

COMMIT;

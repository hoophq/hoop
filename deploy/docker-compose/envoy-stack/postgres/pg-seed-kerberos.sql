-- The Postgres role that a Kerberos principal maps onto.
--
-- Kerberos proves who the client is; this decides whether that person may open
-- a session. With include_realm=1 in pg_hba the name Postgres looks up carries
-- the realm, so the role is "alice@HOOP.TEST" and a bare "alice" never matches.
--
-- Unlike SQL Server, Postgres performs no directory lookup here: it compares
-- the authenticated principal against pg_authid, a local table. That is why
-- this lane needs only a keytab, while the MSSQL lane needed a whole domain
-- controller before CREATE LOGIN would work.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'alice@HOOP.TEST') THEN
        CREATE ROLE "alice@HOOP.TEST" LOGIN;
    END IF;
END
$$;

GRANT CONNECT ON DATABASE appdb TO "alice@HOOP.TEST";
GRANT USAGE ON SCHEMA public TO "alice@HOOP.TEST";

-- Deliberately generous, so the guardrail is the only thing standing between
-- alice and a DELETE. A rule that merely repeats what the database already
-- forbids demonstrates nothing.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "alice@HOOP.TEST";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO "alice@HOOP.TEST";

SELECT 'kerberos role ready' AS status;

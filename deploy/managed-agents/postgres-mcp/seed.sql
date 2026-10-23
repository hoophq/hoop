CREATE TABLE customers (id int PRIMARY KEY, name text, email text, plan text);
INSERT INTO customers VALUES
  (1, 'Ada Lovelace', 'ada@example.com',   'pro'),
  (2, 'Grace Hopper', 'grace@example.org', 'team'),
  (3, 'Alan Turing',  'alan@example.net',  'free');

-- dbhub's login. The sidecar refuses writes first; this login refuses what
-- the sidecar's classifier misses (ENG-803).
CREATE ROLE agent LOGIN PASSWORD 'agent-demo-password';
GRANT SELECT ON customers TO agent;
-- PUBLIC may create temporary tables and large objects by default, and a
-- SELECT can do both.
REVOKE TEMPORARY ON DATABASE appdb FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION lo_creat(integer), lo_create(oid), lo_from_bytea(oid, bytea) FROM PUBLIC;
-- Read-only by default, for writing functions nobody listed here. These
-- settings are defaults: set_config() in a SELECT can change them.
ALTER ROLE agent SET default_transaction_read_only = on;
ALTER ROLE agent SET statement_timeout = '30s';
ALTER ROLE agent SET idle_in_transaction_session_timeout = '30s';

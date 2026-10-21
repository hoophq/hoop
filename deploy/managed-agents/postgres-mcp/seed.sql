CREATE TABLE customers (id int PRIMARY KEY, name text, email text, plan text);
INSERT INTO customers VALUES
  (1, 'Ada Lovelace', 'ada@example.com',   'pro'),
  (2, 'Grace Hopper', 'grace@example.org', 'team'),
  (3, 'Alan Turing',  'alan@example.net',  'free');

-- dbhub's login. The sidecar refuses writes first; this login refuses what
-- the sidecar's classifier misses (ENG-803): SELECT only, and no temporary
-- tables, which PUBLIC may create by default.
CREATE ROLE agent LOGIN PASSWORD 'agent-demo-password';
GRANT SELECT ON customers TO agent;
REVOKE TEMPORARY ON DATABASE appdb FROM PUBLIC;
-- pg_sleep and an open transaction are not writes. These limits end them,
-- unless set_config() in a SELECT changes them for the session.
ALTER ROLE agent SET statement_timeout = '30s';
ALTER ROLE agent SET idle_in_transaction_session_timeout = '30s';

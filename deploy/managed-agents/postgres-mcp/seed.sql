CREATE TABLE customers (id int PRIMARY KEY, name text, email text, plan text);
INSERT INTO customers VALUES
  (1, 'Ada Lovelace', 'ada@example.com',   'pro'),
  (2, 'Grace Hopper', 'grace@example.org', 'team'),
  (3, 'Alan Turing',  'alan@example.net',  'free');

-- dbhub's login. Not a superuser. It may write, so the sidecar alone refuses
-- the demo's DELETE. On your database, grant SELECT only.
CREATE ROLE agent LOGIN PASSWORD 'agent-demo-password';
GRANT SELECT, INSERT, UPDATE, DELETE ON customers TO agent;
-- pg_sleep and an open transaction are not writes, so the sidecar admits
-- them. These limits end them.
ALTER ROLE agent SET statement_timeout = '30s';
ALTER ROLE agent SET idle_in_transaction_session_timeout = '30s';

-- The read-only role the "ro" pool entry logs in as. appuser (the "rw"
-- entry) is the image's superuser from POSTGRES_USER. Neither name is ever
-- typed by a client: the sidecar picks one from the caller's group.
CREATE ROLE app_ro LOGIN PASSWORD 'ro-secret';
GRANT CONNECT ON DATABASE appdb TO app_ro;
GRANT USAGE ON SCHEMA public TO app_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO app_ro;

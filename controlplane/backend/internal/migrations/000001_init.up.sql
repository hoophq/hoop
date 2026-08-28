-- The control plane owns its own Postgres schema so it can share a database
-- with the gateway without table-name collisions.
--
-- No tables yet, on purpose. Each is created by the migration of the
-- component that owns it (desiredstate: sidecar configs and the generation
-- counter; adminauth: admin accounts and sessions; sidecarauth: sidecar
-- credentials and revocations), so components can be reverted independently.
-- inventory is deliberately absent: it is in-memory by design.
CREATE SCHEMA IF NOT EXISTS controlplane;

BEGIN;
SET search_path TO private;

DROP INDEX IF EXISTS idx_event_dispatches_processing;
DROP INDEX IF EXISTS idx_event_dispatches_sub_created;
DROP INDEX IF EXISTS idx_event_dispatches_pending;
DROP TABLE IF EXISTS event_dispatches;

DROP INDEX IF EXISTS idx_event_subscriptions_event_types_gin;
DROP INDEX IF EXISTS idx_event_subscriptions_org_status;
DROP TABLE IF EXISTS event_subscriptions;

DROP INDEX IF EXISTS idx_events_idempotency;
DROP INDEX IF EXISTS idx_events_org_type_occurred;
DROP TABLE IF EXISTS events;

COMMIT;

BEGIN;

UPDATE private.env_vars ev
SET envs = ev.envs::jsonb - 'envvar:LD_LIBRARY_PATH'
WHERE ev.id IN (
  SELECT c.id
  FROM private.connections c
  WHERE c.subtype = 'oracledb'
);

COMMIT;

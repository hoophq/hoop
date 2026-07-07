BEGIN;

SET search_path TO private;

-- Rename existing column
ALTER TABLE resources RENAME COLUMN "type" TO subtype;

-- Add column with type
ALTER TABLE resources ADD COLUMN type enum_connection_type;

-- Fill the new column based on existing data
UPDATE resources AS r
  SET type = c.type
  FROM connections AS c
  WHERE c.resource_name = r.name
    AND c.org_id = r.org_id;

-- Set default value for rows where type is still NULL
UPDATE resources SET type = 'custom' WHERE type IS NULL;

-- Make the new column NOT NULL
ALTER TABLE resources ALTER COLUMN type SET NOT NULL;

COMMIT;

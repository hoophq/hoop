-- very old legacy views cleanup - versions before 1.35.22
-- the migration will fail if those view exists
DROP VIEW IF EXISTS public.user_groups CASCADE;
DROP VIEW IF EXISTS public_a.user_groups CASCADE;
DROP VIEW IF EXISTS public_b.user_groups CASCADE;

-- modify column size
ALTER TABLE private.user_groups ALTER COLUMN name TYPE VARCHAR(180);
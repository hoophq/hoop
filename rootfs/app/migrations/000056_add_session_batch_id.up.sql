BEGIN;

SET search_path TO private;

-- Add session_batch_id column to sessions table
ALTER TABLE sessions 
ADD COLUMN session_batch_id VARCHAR(255) NULL;

-- Add index for filtering performance
CREATE INDEX IF NOT EXISTS index_sessions_batch_id 
ON sessions(org_id, session_batch_id) 
WHERE session_batch_id IS NOT NULL;

COMMIT;


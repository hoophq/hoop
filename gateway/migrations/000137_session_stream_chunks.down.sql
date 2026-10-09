BEGIN;
SET search_path TO private;

-- Fold the chunks into their blobs first, so the rollback keeps every entry.
UPDATE blobs AS b
SET blob_stream = b.blob_stream || c.entries
FROM (
    SELECT ch.org_id, ch.blob_id, jsonb_agg(e.entry ORDER BY ch.id, e.ord) AS entries
    FROM session_stream_chunks AS ch,
        jsonb_array_elements(ch.entries) WITH ORDINALITY AS e(entry, ord)
    GROUP BY ch.org_id, ch.blob_id
) AS c
WHERE b.org_id = c.org_id AND b.id = c.blob_id;

DROP TABLE IF EXISTS session_stream_chunks;

COMMIT;

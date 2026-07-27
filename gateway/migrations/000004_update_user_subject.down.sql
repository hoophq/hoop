BEGIN;

SET search_path TO private;

DROP FUNCTION public.update_users(id UUID, org_id UUID, subject TEXT, email TEXT, name TEXT, verified BOOLEAN, status enum_user_status, slack_id TEXT, groups VARCHAR(100)[]);
CREATE FUNCTION
    public.update_users(id UUID, org_id UUID, subject TEXT, email TEXT, name TEXT, verified BOOLEAN, status enum_user_status, slack_id TEXT, groups VARCHAR(100)[]) RETURNS SETOF public.users AS $$
    WITH params AS (
        SELECT
            id AS id,
            org_id AS org_id,
            subject AS subject,
            email AS email,
            name AS name,
            verified AS verified,
            status AS status,
            slack_id AS slack_id,
            groups AS groups
    ), upsert_users AS (
        INSERT INTO public.users (id, org_id, subject, email, name, verified, status, slack_id)
            (SELECT id, org_id, subject, email, name, verified, status, slack_id FROM params)
        ON CONFLICT (subject)
            DO UPDATE SET
                name = (SELECT name FROM params),
                status = (SELECT status FROM params),
                verified = (SELECT verified FROM params),
                slack_id = (SELECT slack_id FROM params),
                updated_at = NOW()
        RETURNING *
    ), grps AS (
        SELECT org_id AS org_id, id AS user_id, UNNEST(groups) AS name
    ), update_user_groups AS (
        INSERT INTO public.user_groups (org_id, user_id, name)
            SELECT org_id, user_id, name FROM grps
            ON CONFLICT DO NOTHING
    ), del_grousp AS (
        DELETE FROM public.user_groups
        WHERE user_id = id
        AND org_id = org_id
        AND name NOT IN (SELECT name FROM grps)
    )
    SELECT * FROM upsert_users
$$ LANGUAGE SQL;

COMMIT;

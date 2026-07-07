BEGIN;

SET search_path TO private;

ALTER TABLE private.users ADD COLUMN picture VARCHAR(2048) NULL;

DROP FUNCTION IF EXISTS public.update_users;
DROP FUNCTION IF EXISTS public.groups(public.users);
DROP VIEW public.users;

CREATE VIEW public.users AS
    SELECT
        id, org_id, subject, email, name, picture, verified, status, slack_id, created_at, updated_at
    FROM users;

CREATE FUNCTION public.groups(public.users) RETURNS TEXT[] AS $$
    SELECT ARRAY(
        SELECT name FROM public.user_groups
        WHERE org_id = $1.org_id
        AND user_id = $1.id
    )
$$ LANGUAGE SQL;

CREATE FUNCTION public.update_users(params json) RETURNS SETOF public.users AS $$
    WITH user_input AS (
        SELECT
            (params->>'id')::UUID AS id,
            (params->>'org_id')::UUID AS org_id,
            params->>'subject' AS subject,
            params->>'email' AS email,
            params->>'name' AS name,
            params->>'picture' AS picture,
            (params->>'verified')::BOOL AS verified,
            (params->>'status')::private.enum_user_status AS status,
            params->>'slack_id' AS slack_id
    ), upsert_users AS (
        INSERT INTO public.users (id, org_id, subject, email, name, picture, verified, status, slack_id)
            (SELECT id, org_id, subject, email, name, picture, verified, status, slack_id FROM user_input)
        ON CONFLICT (id)
            DO UPDATE SET
                subject = (SELECT subject FROM user_input),
                name = (SELECT name FROM user_input),
                status = (SELECT status FROM user_input),
                verified = (SELECT verified FROM user_input),
                slack_id = (SELECT slack_id FROM user_input),
                picture = (SELECT picture FROM user_input),
                updated_at = NOW()
        RETURNING *
    ), grps AS (
        SELECT
            (params->>'org_id')::UUID AS org_id,
            (params->>'id')::UUID AS user_id,
            jsonb_array_elements_text((params->>'groups')::JSONB) AS name
    ), update_user_groups AS (
        INSERT INTO public.user_groups (org_id, user_id, name)
            SELECT org_id, user_id, name FROM grps
            ON CONFLICT DO NOTHING
    ), del_grousp AS (
        DELETE FROM public.user_groups
        WHERE user_id = (params->>'id')::UUID
        AND org_id = (params->>'org_id')::UUID
        AND name NOT IN (SELECT name::TEXT FROM grps)
    )
    SELECT * FROM upsert_users
$$ LANGUAGE SQL;

COMMIT;

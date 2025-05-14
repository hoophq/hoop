BEGIN;

INSERT INTO private.user_organizations (user_id, org_id, role)
SELECT id, org_id, 'member' 
FROM private.users;

UPDATE private.user_organizations uo
SET role = 'admin'
FROM private.user_groups ug
WHERE uo.user_id = ug.user_id AND ug.name = 'admin';

INSERT INTO private.user_preferences (user_id, active_org_id)
SELECT id, org_id 
FROM private.users;

COMMIT;

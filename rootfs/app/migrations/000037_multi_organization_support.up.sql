BEGIN;

CREATE TABLE private.user_organizations(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES private.users(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES private.orgs(id) ON DELETE CASCADE,
    role VARCHAR(50) NOT NULL DEFAULT 'member',
    created_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(user_id, org_id)
);

CREATE TABLE private.user_preferences(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES private.users(id) ON DELETE CASCADE,
    active_org_id UUID NOT NULL REFERENCES private.orgs(id) ON DELETE CASCADE,
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(user_id)
);

CREATE VIEW public.user_organizations AS
    SELECT id, user_id, org_id, role, created_at FROM private.user_organizations;

CREATE VIEW public.user_preferences AS
    SELECT id, user_id, active_org_id, updated_at FROM private.user_preferences;

COMMIT;

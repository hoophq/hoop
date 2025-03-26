BEGIN;

SET search_path TO private;

CREATE TABLE connection_tags(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    key VARCHAR(128) NOT NULL,
    value VARCHAR(255) NOT NULL DEFAULT '',

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(org_id, key, value)
);

CREATE TABLE connection_tags_association(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NULL REFERENCES orgs (id),

    tag_id UUID NOT NULL REFERENCES connection_tags (id) ON DELETE CASCADE,
    connection_id UUID NOT NULL REFERENCES connections (id) ON DELETE CASCADE,

    created_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(tag_id, connection_id)
);

COMMIT;

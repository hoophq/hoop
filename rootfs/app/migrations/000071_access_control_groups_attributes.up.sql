BEGIN;

SET search_path TO private;

CREATE TABLE IF NOT EXISTS access_control_groups_attributes (
    org_id UUID NOT NULL,
    group_name VARCHAR(100) NOT NULL,
    attribute_name VARCHAR(255) NOT NULL,
    PRIMARY KEY (org_id, group_name, attribute_name),
    FOREIGN KEY (org_id, attribute_name) REFERENCES attributes(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);

COMMIT;

-- Upstream the DBA cares about. Small enough to eyeball, real enough that a
-- blocked DELETE is obviously a policy decision and not an empty-table no-op.

CREATE TABLE customers (
    id      serial PRIMARY KEY,
    name    text NOT NULL,
    email   text NOT NULL,
    ssn     text NOT NULL
);

INSERT INTO customers (name, email, ssn) VALUES
    ('Ada Lovelace',   'ada@example.com',    '123-45-6789'),
    ('Grace Hopper',   'grace@example.com',  '987-65-4321'),
    ('Alan Turing',    'alan@example.com',   '555-12-3456');

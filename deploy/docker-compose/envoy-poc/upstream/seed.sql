-- Upstream the DBA cares about. Small enough to eyeball, real enough that a
-- blocked DELETE is obviously a policy decision and not an empty-table no-op.

CREATE TABLE customers (
    id      serial PRIMARY KEY,
    name    text NOT NULL,
    email   text NOT NULL,
    ssn     text NOT NULL,
    -- Non-US identifiers, which the eight built-in mask detectors cannot
    -- find. They are the reason the hoop-inspect-pii build exists: both
    -- carry a real checksum, so a detector either verifies them or drops
    -- them, and a regex would have to guess.
    cpf     text NOT NULL,   -- Brazilian taxpayer id, mod-11
    iban    text NOT NULL    -- bank account, ISO 7064 mod-97
);

-- The three SSNs are deliberately different shapes. 555-12-3456 is an
-- ordinary one. 123-45-6789 and 987-65-4321 are the sequential and
-- descending runs every test fixture uses, and alcatraz REJECTS them as
-- obvious placeholders while the built-in regex masks them. Keeping all
-- three in the demo makes that disagreement visible instead of hiding it.
INSERT INTO customers (name, email, ssn, cpf, iban) VALUES
    ('Ada Lovelace',   'ada@example.com',    '123-45-6789', '111.444.777-35', 'GB82WEST12345698765432'),
    ('Grace Hopper',   'grace@example.com',  '987-65-4321', '529.982.247-25', 'DE89370400440532013000'),
    ('Alan Turing',    'alan@example.com',   '555-12-3456', '390.533.447-05', 'FR1420041010050500013M02606');

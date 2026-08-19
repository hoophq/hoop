-- The database Metabase reports on. Two tables, two jobs.
--
-- `customers` is three rows, so a masked column is obvious on one screen.
-- `events` is 5,000 rows, to outrun maxDecodedRows (1,000, in
-- codec/postgres/response.go). That constant bounds audit decoding only, and
-- ./demo.sh exports all 5,000 rows to prove masking does not stop with it.
-- See ./README.md, "Masking does not stop at row 1000".

CREATE TABLE customers (
    id      serial PRIMARY KEY,
    name    text NOT NULL,
    email   text NOT NULL,
    ssn     text NOT NULL,
    -- Non-US identifiers, invisible to a US-centric regex set. Both carry a
    -- real checksum (mod-11 on CPF, ISO 7064 mod-97 on IBAN), so a detector
    -- can verify them instead of guessing.
    cpf     text NOT NULL,   -- Brazilian taxpayer id
    iban    text NOT NULL    -- bank account
);

-- The three SSNs disagree on purpose. 555-12-3456 is ordinary. 123-45-6789
-- and 987-65-4321 are the sequential and descending runs every fixture uses,
-- and alcatraz rejects both as placeholders, so an entity rule alone would
-- leave two of the three in the clear. Hence the column rule beside it in
-- ../hoopinspect/config.yaml.
INSERT INTO customers (name, email, ssn, cpf, iban) VALUES
    ('Ada Lovelace', 'ada@example.com',   '123-45-6789', '111.444.777-35', 'GB82WEST12345698765432'),
    ('Grace Hopper', 'grace@example.com', '987-65-4321', '529.982.247-25', 'DE89370400440532013000'),
    ('Alan Turing',  'alan@example.com',  '555-12-3456', '390.533.447-05', 'FR1420041010050500013M02606');

-- One maskable value per row, so ./demo.sh can assert exactly: 5,000 rows in,
-- 5,000 redactions out, zero surviving '@'.
CREATE TABLE events (
    id           serial PRIMARY KEY,
    occurred_at  timestamp NOT NULL,
    actor_email  text NOT NULL,
    action       text NOT NULL,
    amount_cents integer NOT NULL
);

-- No random(), so two runs export byte-identical CSVs and a failed assertion
-- means a regression rather than a reseeded table.
INSERT INTO events (occurred_at, actor_email, action, amount_cents)
SELECT
    timestamp '2026-01-01 00:00:00' + (n || ' minutes')::interval,
    'user' || lpad(n::text, 5, '0') || '@example.com',
    (ARRAY['login', 'export', 'query', 'share'])[1 + (n % 4)],
    (n * 37) % 100000
FROM generate_series(1, 5000) AS n;

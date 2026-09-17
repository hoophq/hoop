-- Upstream the DBA cares about. Same three rows as the envoy-stack fixture,
-- in ClickHouse's dialect, so a blocked DELETE is obviously a policy
-- decision and not an empty-table no-op.
--
-- Runs once from /docker-entrypoint-initdb.d, over the native protocol on
-- localhost, before the server accepts anything from the network.

CREATE DATABASE IF NOT EXISTS appdb;

CREATE TABLE appdb.customers
(
    id    UInt32,
    name  String,
    email String,
    ssn   String,
    cpf   String,  -- Brazilian taxpayer id, mod-11
    iban  String   -- bank account, ISO 7064 mod-97
)
ENGINE = MergeTree
ORDER BY id;

INSERT INTO appdb.customers (id, name, email, ssn, cpf, iban) VALUES
    (1, 'Ada Lovelace', 'ada@example.com',   '123-45-6789', '111.444.777-35', 'GB82WEST12345698765432'),
    (2, 'Grace Hopper', 'grace@example.com', '987-65-4321', '529.982.247-25', 'DE89370400440532013000'),
    (3, 'Alan Turing',  'alan@example.com',  '555-12-3456', '390.533.447-05', 'FR1420041010050500013M02606');

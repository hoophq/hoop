-- Upstream the DBA cares about. Same three rows as the envoy-stack fixture,
-- in MySQL's dialect, so a blocked DELETE is obviously a policy decision and
-- not an empty-table no-op.
--
-- Runs once against MYSQL_DATABASE (appdb) from /docker-entrypoint-initdb.d.

CREATE TABLE customers (
    id    INT AUTO_INCREMENT PRIMARY KEY,
    name  VARCHAR(100) NOT NULL,
    email VARCHAR(100) NOT NULL,
    ssn   VARCHAR(11)  NOT NULL,
    cpf   VARCHAR(14)  NOT NULL, -- Brazilian taxpayer id, mod-11
    iban  VARCHAR(34)  NOT NULL  -- bank account, ISO 7064 mod-97
);

INSERT INTO customers (name, email, ssn, cpf, iban) VALUES
    ('Ada Lovelace', 'ada@example.com',   '123-45-6789', '111.444.777-35', 'GB82WEST12345698765432'),
    ('Grace Hopper', 'grace@example.com', '987-65-4321', '529.982.247-25', 'DE89370400440532013000'),
    ('Alan Turing',  'alan@example.com',  '555-12-3456', '390.533.447-05', 'FR1420041010050500013M02606');

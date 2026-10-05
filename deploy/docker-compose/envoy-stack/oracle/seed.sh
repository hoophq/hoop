#!/bin/bash
# Upstream the DBA cares about. Same three rows as the envoy-stack fixture,
# in Oracle's dialect, so a blocked DELETE is obviously a policy decision and
# not an empty-table no-op.
#
# The image runs this once from /container-entrypoint-initdb.d, after it
# creates APP_USER. ORACLE_SERVICE names the pluggable database: FREEPDB1 on
# Oracle Free 23ai, XEPDB1 on Oracle XE 21c.
set -euo pipefail

sqlplus -S / as sysdba <<EOF
WHENEVER SQLERROR EXIT FAILURE
ALTER SESSION SET CONTAINER = ${ORACLE_SERVICE};

CREATE TABLE appuser.customers (
    id    NUMBER(10)    PRIMARY KEY,
    name  VARCHAR2(100) NOT NULL,
    email VARCHAR2(100) NOT NULL,
    ssn   VARCHAR2(11)  NOT NULL,
    cpf   VARCHAR2(14)  NOT NULL, -- Brazilian taxpayer id, mod-11
    iban  VARCHAR2(34)  NOT NULL  -- bank account, ISO 7064 mod-97
);

INSERT INTO appuser.customers VALUES
    (1, 'Ada Lovelace', 'ada@example.com',   '123-45-6789', '111.444.777-35', 'GB82WEST12345698765432');
INSERT INTO appuser.customers VALUES
    (2, 'Grace Hopper', 'grace@example.com', '987-65-4321', '529.982.247-25', 'DE89370400440532013000');
INSERT INTO appuser.customers VALUES
    (3, 'Alan Turing',  'alan@example.com',  '555-12-3456', '390.533.447-05', 'FR1420041010050500013M02606');
COMMIT;
EOF

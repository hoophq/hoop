-- Seed managed default rulepacks (and their rules / attributes / associations)
-- for every existing organization.
--
-- This migration is idempotent: re-running it (or running it after new orgs
-- are created and a later migration triggers a re-seed) will not produce
-- duplicates, thanks to ON CONFLICT clauses that match the actual unique
-- constraints on each table:
--
--   private.rulepacks                  UNIQUE (org_id, display_name)
--   private.guardrail_rules            UNIQUE (org_id, name)
--   private.attributes                 UNIQUE INDEX (org_id, name)
--   private.guardrail_rules_attributes PRIMARY KEY (org_id, guardrail_rule_name, attribute_name)
--
-- Naming contract (MUST stay in sync with gateway/services/rulepacks.go):
--   * Stored rule name = 'rp_' || substring(rulepack_id::text, 1, 8) || '__' || <short>
--     This matches services.RulepackRuleNamePrefix(rulepackID) + <short>, which
--     uses the first 8 hex chars of the rulepack UUID as a per-rulepack prefix.
--     Since each org gets its own rulepack UUID, each org's stored rule names
--     will use that org's UUID prefix (NOT the catalog's family_id).
--   * `family_id` in the temp catalogs below is ONLY a grouping key linking a
--     rule entry to its parent rulepack entry. It is never written to the DB.

BEGIN;

SET search_path TO private;

-- ---------------------------------------------------------------------------
-- 1. Catalog of rulepacks to seed (one row per logical rulepack).
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE _rulepack_catalog (
    family_id        TEXT PRIMARY KEY,
    display_name     TEXT NOT NULL,
    tags             TEXT[] NOT NULL,
    attribute_name   TEXT NOT NULL
) ON COMMIT DROP;

INSERT INTO _rulepack_catalog (family_id, display_name, tags, attribute_name) VALUES
    ('pg',     'PostgreSQL Security Pack',  ARRAY['postgres','database'],       'rulepack_postgresql_security_pack'),
    ('mysql',  'MySQL Security Pack',       ARRAY['mysql','database'],          'rulepack_mysql_security_pack'),
    ('mongo',  'MongoDB Security Pack',     ARRAY['mongodb','database'],        'rulepack_mongodb_security_pack'),
    ('ddb',    'DynamoDB Security Pack',    ARRAY['dynamodb','database','aws'], 'rulepack_dynamodb_security_pack'),
    ('oracle', 'Oracle DB Security Pack',   ARRAY['oracledb','database'],       'rulepack_oracle_db_security_pack'),
    ('mssql',  'MSSQL Security Pack',       ARRAY['mssql','database'],          'rulepack_mssql_security_pack'),
    ('claude', 'Claude Code Security Pack', ARRAY['claude-code','ai'],          'rulepack_claude_code_security_pack'),
    ('aws',    'AWS CLI Security Pack',     ARRAY['aws-cli','aws'],             'rulepack_aws_cli_security_pack'),
    ('k8s',    'Kubernetes Security Pack',  ARRAY['kubernetes'],                'rulepack_kubernetes_security_pack');

-- ---------------------------------------------------------------------------
-- 2. Catalog of guardrail rules. `family_id` links each rule to its parent
--    rulepack catalog entry; it is NOT written to the DB. The stored rule
--    name is built per-org from the actual rulepack UUID (see step 4).
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE _rule_catalog (
    family_id    TEXT NOT NULL,
    short_name   TEXT NOT NULL,
    description  TEXT NOT NULL,
    input        JSONB NOT NULL,
    output       JSONB NOT NULL,
    PRIMARY KEY (family_id, short_name)
) ON COMMIT DROP;

INSERT INTO _rule_catalog (family_id, short_name, description, input, output) VALUES
    -- PostgreSQL
    ('pg', 'pg-block-unsafe-write',
        'PostgreSQL: block UPDATE/DELETE without WHERE',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('pg',     'pg-block-destructive-ddl',
        'PostgreSQL: block DROP/TRUNCATE on tables, schemas, databases',
        '{"rules": [{"type": "deny_words_list", "words": ["DROP TABLE", "TRUNCATE", "DROP DATABASE", "DROP SCHEMA"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('pg',     'pg-mask-pgcrypto-output',
        'PostgreSQL: redact filesystem-access function output',
        '{"rules": []}'::jsonb,
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(pg_read_file|pg_ls_dir|pg_stat_file)\\b"}]}'::jsonb),

    -- MySQL
    ('mysql',  'mysql-block-unsafe-write',
        'MySQL: block UPDATE/DELETE without WHERE',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('mysql',  'mysql-block-grant-changes',
        'MySQL: block privilege and authentication changes',
        '{"rules": [{"type": "deny_words_list", "words": ["GRANT", "REVOKE", "CREATE USER", "DROP USER", "SET PASSWORD"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('mysql',  'mysql-block-load-infile',
        'MySQL: block filesystem read/write via SQL',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bLOAD\\s+DATA\\b|\\bINTO\\s+OUTFILE\\b|\\bINTO\\s+DUMPFILE\\b"}]}'::jsonb,
        '{"rules": []}'::jsonb),

    -- MongoDB
    ('mongo',  'mongo-block-drop',
        'MongoDB: block collection and database drops',
        '{"rules": [{"type": "deny_words_list", "words": ["dropDatabase", "db.dropDatabase", ".drop(", "dropAllUsers"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('mongo',  'mongo-block-unfiltered-write',
        'MongoDB: block updateMany/deleteMany with empty filter',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\.(updateMany|deleteMany)\\s*\\(\\s*\\{\\s*\\}"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('mongo',  'mongo-block-eval',
        'MongoDB: block server-side JavaScript execution',
        '{"rules": [{"type": "deny_words_list", "words": ["db.eval", "$where", "mapReduce"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),

    -- DynamoDB
    ('ddb',    'ddb-block-table-delete',
        'DynamoDB: block table deletion API calls',
        '{"rules": [{"type": "deny_words_list", "words": ["DeleteTable", "delete-table"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('ddb',    'ddb-block-full-scan',
        'DynamoDB: block Scan without FilterExpression',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bScan\\b(?!.*\\bFilterExpression\\b)"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('ddb',    'ddb-block-batch-purge',
        'DynamoDB: block bulk deletes via BatchWriteItem',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)BatchWriteItem.*DeleteRequest"}]}'::jsonb,
        '{"rules": []}'::jsonb),

    -- Oracle
    ('oracle', 'oracle-block-unsafe-write',
        'Oracle: block UPDATE/DELETE without WHERE',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('oracle', 'oracle-block-shutdown',
        'Oracle: block instance/system control statements',
        '{"rules": [{"type": "deny_words_list", "words": ["SHUTDOWN", "STARTUP", "ALTER SYSTEM", "ALTER DATABASE"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('oracle', 'oracle-block-utl-packages',
        'Oracle: block filesystem and network UTL/DBMS packages',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(UTL_FILE|UTL_HTTP|UTL_SMTP|DBMS_LOB\\.LOADFROMFILE)\\b"}]}'::jsonb,
        '{"rules": []}'::jsonb),

    -- MSSQL
    ('mssql',  'mssql-block-unsafe-write',
        'MSSQL: block UPDATE/DELETE without WHERE',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('mssql',  'mssql-block-xp-cmdshell',
        'MSSQL: block xp_cmdshell and OLE automation procs',
        '{"rules": [{"type": "deny_words_list", "words": ["xp_cmdshell", "sp_configure", "EXEC sp_OACreate"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('mssql',  'mssql-block-bulk-io',
        'MSSQL: block BULK INSERT and OPENROWSET I/O',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bBULK\\s+INSERT\\b|\\bOPENROWSET\\b|\\bOPENDATASOURCE\\b"}]}'::jsonb,
        '{"rules": []}'::jsonb),

    -- Claude Code
    ('claude', 'claude-block-shell-exec',
        'Claude Code: block destructive/piped-shell prompt patterns',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(rm\\s+-rf\\s+/|sudo\\s+|curl\\s+.*\\|\\s*sh|wget\\s+.*\\|\\s*sh)\\b"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('claude', 'claude-mask-pii-output',
        'Claude Code: redact SSN/email/PAN in model output',
        '{"rules": []}'::jsonb,
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(\\d{3}-\\d{2}-\\d{4}|[\\w.+-]+@[\\w-]+\\.[\\w.-]+|\\d{13,19})\\b"}]}'::jsonb),
    ('claude', 'claude-block-prompt-injection',
        'Claude Code: block common prompt-injection phrases',
        '{"rules": [{"type": "deny_words_list", "words": ["ignore previous instructions", "disregard the system prompt", "you are now", "developer mode"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),

    -- AWS CLI
    ('aws',    'aws-block-iam-mutations',
        'AWS CLI: block IAM identity/policy mutations',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(iam\\s+(create|delete|attach|put)-(user|policy|role|access-key))\\b"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('aws',    'aws-block-account-destruction',
        'AWS CLI: block resource teardown commands',
        '{"rules": [{"type": "deny_words_list", "words": ["delete-bucket", "terminate-instances", "delete-db-instance", "delete-cluster", "delete-stack"], "pattern_regex": ""}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('aws',    'aws-mask-credentials-output',
        'AWS CLI: redact access keys in output',
        '{"rules": []}'::jsonb,
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(AKIA[0-9A-Z]{16}|aws_secret_access_key\\s*=\\s*\\S+)\\b"}]}'::jsonb),

    -- Kubernetes
    ('k8s',    'k8s-block-destructive',
        'Kubernetes: block cluster-wide deletes',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bkubectl\\s+delete\\s+(ns|namespace|all|crd|nodes?)\\b"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('k8s',    'k8s-block-privileged-exec',
        'Kubernetes: block interactive shells into pods',
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bkubectl\\s+exec\\b.*--\\s*(sh|bash)\\b"}]}'::jsonb,
        '{"rules": []}'::jsonb),
    ('k8s',    'k8s-mask-secret-output',
        'Kubernetes: redact Secret data fields in output',
        '{"rules": []}'::jsonb,
        '{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\"(data|stringData)\":\\s*\\{[^}]*\\}"}]}'::jsonb);

-- ---------------------------------------------------------------------------
-- 3. Insert one rulepack row per (org, catalog entry).
--    UUIDs are generated per row; ON CONFLICT keeps the existing one.
-- ---------------------------------------------------------------------------
INSERT INTO rulepacks (id, org_id, display_name, description, version, tags, is_managed, created_at, updated_at)
SELECT
    gen_random_uuid(),
    o.id,
    c.display_name,
    NULL,
    '0.1.0',
    c.tags,
    true,
    NOW(),
    NOW()
FROM orgs o
CROSS JOIN _rulepack_catalog c
ON CONFLICT (org_id, display_name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 4. Insert one guardrail rule row per (org, rule catalog entry), linking
--    each to the rulepack row that was just inserted (or already existed)
--    for that org.
-- ---------------------------------------------------------------------------
INSERT INTO guardrail_rules (id, org_id, name, input, output, created_at, updated_at, description, rulepack_id)
SELECT
    uuid_generate_v4(),
    o.id,
    'rp_' || substring(rp.id::text, 1, 8) || '__' || r.short_name,
    r.input,
    r.output,
    NOW(),
    NOW(),
    r.description,
    rp.id
FROM orgs o
CROSS JOIN _rule_catalog r
JOIN _rulepack_catalog c  ON c.family_id = r.family_id
JOIN rulepacks       rp  ON rp.org_id = o.id AND rp.display_name = c.display_name
ON CONFLICT (org_id, name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 5. Insert one attribute row per (org, rulepack catalog entry), linking it
--    to the rulepack row for that org.
-- ---------------------------------------------------------------------------
INSERT INTO attributes (id, org_id, name, description, created_at, rulepack_id)
SELECT
    gen_random_uuid(),
    o.id,
    c.attribute_name,
    NULL,
    NOW(),
    rp.id
FROM orgs o
CROSS JOIN _rulepack_catalog c
JOIN rulepacks rp ON rp.org_id = o.id AND rp.display_name = c.display_name
ON CONFLICT (org_id, name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 6. Associate each guardrail rule with its rulepack attribute.
--    The junction table uses (org_id, guardrail_rule_name, attribute_name)
--    as its composite primary key, so ON CONFLICT DO NOTHING is enough.
-- ---------------------------------------------------------------------------
INSERT INTO guardrail_rules_attributes (org_id, guardrail_rule_name, attribute_name)
SELECT
    o.id,
    'rp_' || substring(rp.id::text, 1, 8) || '__' || r.short_name,
    c.attribute_name
FROM orgs o
CROSS JOIN _rule_catalog r
JOIN _rulepack_catalog c  ON c.family_id = r.family_id
JOIN rulepacks       rp  ON rp.org_id = o.id AND rp.display_name = c.display_name
ON CONFLICT DO NOTHING;

COMMIT;

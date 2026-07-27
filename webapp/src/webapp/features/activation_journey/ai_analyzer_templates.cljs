(ns webapp.features.activation-journey.ai-analyzer-templates)

;; Curated AI Session Analyzer rule templates, indexed by connection subtype.
;; Source of truth: ai-session-analyzer-templates.json (Feature Specs |
;; Product Activation Journey / EVL-69). Generated from that file:
;; - :title is derived from the rule name (subtype prefix stripped);
;;   :card-description is the leading clause of the rule description.
;; - :risk_evaluation is normalized from the legacy flat *_action keys to
;;   the nested shape the rule form posts to /ai/session-analyzer/rules.
;; Everything except :title/:card-description is the POST request body;
;; :connection_names is filled in by the activation-journey deep link.
(def templates-by-subtype
  {
   "postgres"
   [
    {:title "Block destructive DDL"
     :card-description "Block destructive schema/data operations on a PostgreSQL connection."
     :name "postgres-block-destructive-ddl"
     :description "Block destructive schema/data operations on a PostgreSQL connection: DROP TABLE/DATABASE/SCHEMA, TRUNCATE, ALTER TABLE ... DROP COLUMN, and UPDATE/DELETE statements with no WHERE clause. Real scenario: an engineer debugging in prod runs `DELETE FROM orders;` without a filter and wipes the table."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Protect PII bulk reads"
     :card-description "Block bulk or unbounded reads of sensitive tables (customers, users, payments, auth tokens)."
     :name "postgres-protect-pii-bulk-reads"
     :description "Block bulk or unbounded reads of sensitive tables (customers, users, payments, auth tokens): SELECT * with no LIMIT/WHERE, COPY ... TO, or pg_dump-style exports of PII columns. Real scenario: a support engineer runs `SELECT * FROM customers;` and exports millions of PII rows."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard privilege changes"
     :card-description "Block privilege-escalation and account changes."
     :name "postgres-guard-privilege-changes"
     :description "Block privilege-escalation and account changes: GRANT/REVOKE, CREATE/ALTER ROLE, ALTER USER ... SUPERUSER, and changes to pg_hba or default privileges. Real scenario: a developer grants themselves SUPERUSER mid-session to bypass row-level security."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "mysql"
   [
    {:title "Block schema destruction"
     :card-description "Block DROP DATABASE/TABLE, TRUNCATE, and DROP INDEX on a MySQL connection."
     :name "mysql-block-schema-destruction"
     :description "Block DROP DATABASE/TABLE, TRUNCATE, and DROP INDEX on a MySQL connection. Real scenario: a cleanup script pasted into prod drops the wrong schema during a 'staging' fix."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent unbounded DML"
     :card-description "Block UPDATE and DELETE statements that lack a WHERE clause, plus multi-table DELETE joins with no filter."
     :name "mysql-prevent-unbounded-dml"
     :description "Block UPDATE and DELETE statements that lack a WHERE clause, plus multi-table DELETE joins with no filter. Real scenario: `UPDATE users SET status='disabled';` accidentally disables every account."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Restrict grants and users"
     :card-description "Block GRANT ALL, CREATE/DROP USER, SET PASSWORD for other users, and FLUSH PRIVILEGES."
     :name "mysql-restrict-grants-and-users"
     :description "Block GRANT ALL, CREATE/DROP USER, SET PASSWORD for other users, and FLUSH PRIVILEGES. Real scenario: an on-call engineer creates a permanent admin user during an incident and never removes it."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "mongodb"
   [
    {:title "Block collection drops"
     :card-description "Block dropDatabase(), <collection>.drop(), and deleteMany({}) / remove({}) with an empty filter."
     :name "mongodb-block-collection-drops"
     :description "Block dropDatabase(), <collection>.drop(), and deleteMany({}) / remove({}) with an empty filter. Real scenario: `db.sessions.deleteMany({})` is run to 'clear test data' against the production cluster."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard mass updates"
     :card-description "Block updateMany/update with an empty or always-true filter and $where with arbitrary JavaScript."
     :name "mongodb-guard-mass-updates"
     :description "Block updateMany/update with an empty or always-true filter and $where with arbitrary JavaScript. Real scenario: `db.users.updateMany({}, {$set:{verified:true}})` flips verification for all users."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Protect user admin"
     :card-description "Block createUser, updateUser, grantRolesToUser, dropAllUsers, and auth/role administration commands."
     :name "mongodb-protect-user-admin"
     :description "Block createUser, updateUser, grantRolesToUser, dropAllUsers, and auth/role administration commands. Real scenario: a contractor grants the `root` role to a shared service account."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "dynamodb"
   [
    {:title "Block table deletion"
     :card-description "Block DeleteTable, DeleteBackup, and disabling point-in-time recovery on a DynamoDB connection."
     :name "dynamodb-block-table-deletion"
     :description "Block DeleteTable, DeleteBackup, and disabling point-in-time recovery on a DynamoDB connection. Real scenario: a misnamed cleanup command issues DeleteTable on the live `orders` table."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard full table scans"
     :card-description "Block full-table Scan operations without a filter or projection and large ExportTableToPointInTime jobs against tables holding PII."
     :name "dynamodb-guard-full-table-scans"
     :description "Block full-table Scan operations without a filter or projection and large ExportTableToPointInTime jobs against tables holding PII. Real scenario: an analyst scans the entire `customers` table, spiking RCUs and cost."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Restrict capacity and config"
     :card-description "Block UpdateTable changes to throughput/billing mode, UpdateContinuousBackups disablement, and GSI deletion."
     :name "dynamodb-restrict-capacity-and-config"
     :description "Block UpdateTable changes to throughput/billing mode, UpdateContinuousBackups disablement, and GSI deletion. Real scenario: provisioned capacity is dropped to 1 RCU on a production table during peak traffic."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "oracledb"
   [
    {:title "Block destructive DDL"
     :card-description "Block DROP TABLE/TABLESPACE/USER, TRUNCATE TABLE, and ALTER TABLE ... DROP on an Oracle connection."
     :name "oracledb-block-destructive-ddl"
     :description "Block DROP TABLE/TABLESPACE/USER, TRUNCATE TABLE, and ALTER TABLE ... DROP on an Oracle connection. Real scenario: a DBA pastes a DROP TABLESPACE from a runbook into the wrong environment."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent mass data export"
     :card-description "Block large SELECT/spool exports and DBMS_DATAPUMP jobs against sensitive tables with no row limit."
     :name "oracledb-prevent-mass-data-export"
     :description "Block large SELECT/spool exports and DBMS_DATAPUMP jobs against sensitive tables with no row limit. Real scenario: an auditor spools the full `salaries` table to a local file."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard privilege grants"
     :card-description "Block GRANT DBA / SYSDBA, CREATE/ALTER USER, and grants of ANY-privilege roles."
     :name "oracledb-guard-privilege-grants"
     :description "Block GRANT DBA / SYSDBA, CREATE/ALTER USER, and grants of ANY-privilege roles. Real scenario: `GRANT DBA TO appuser;` quietly elevates an application account."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "mssql"
   [
    {:title "Block database drops"
     :card-description "Block DROP DATABASE/TABLE, TRUNCATE TABLE, and DELETE without WHERE on a SQL Server connection."
     :name "mssql-block-database-drops"
     :description "Block DROP DATABASE/TABLE, TRUNCATE TABLE, and DELETE without WHERE on a SQL Server connection. Real scenario: a migration script run by hand drops a table that still has live dependencies."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent OS command execution"
     :card-description "Block xp_cmdshell, sp_configure enabling of advanced/risky options, and OLE automation procedures (sp_OACreate)."
     :name "mssql-prevent-os-command-execution"
     :description "Block xp_cmdshell, sp_configure enabling of advanced/risky options, and OLE automation procedures (sp_OACreate). Real scenario: xp_cmdshell is enabled to run shell commands on the DB host."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Restrict logins and roles"
     :card-description "Block CREATE LOGIN, ALTER SERVER ROLE ... ADD MEMBER (sysadmin), and GRANT CONTROL SERVER."
     :name "mssql-restrict-logins-and-roles"
     :description "Block CREATE LOGIN, ALTER SERVER ROLE ... ADD MEMBER (sysadmin), and GRANT CONTROL SERVER. Real scenario: a temporary login is added to the sysadmin role during troubleshooting and forgotten."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "cassandra"
   [
    {:title "Block keyspace drops"
     :card-description "Block DROP KEYSPACE/TABLE and TRUNCATE on a Cassandra connection."
     :name "cassandra-block-keyspace-drops"
     :description "Block DROP KEYSPACE/TABLE and TRUNCATE on a Cassandra connection. Real scenario: `TRUNCATE events;` is run to reset a counter and erases production telemetry."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard allow filtering scans"
     :card-description "Block queries using ALLOW FILTERING or full-partition scans without a partition key, which cause cluster-wide reads."
     :name "cassandra-guard-allow-filtering-scans"
     :description "Block queries using ALLOW FILTERING or full-partition scans without a partition key, which cause cluster-wide reads. Real scenario: an unbounded `SELECT ... ALLOW FILTERING` overloads every node."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Restrict roles and permissions"
     :card-description "Block CREATE/ALTER ROLE, GRANT, and changes to system_auth."
     :name "cassandra-restrict-roles-and-permissions"
     :description "Block CREATE/ALTER ROLE, GRANT, and changes to system_auth. Real scenario: a new superuser role is created to bypass per-keyspace permissions."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "bigquery"
   [
    {:title "Block dataset deletion"
     :card-description "Block DROP TABLE/VIEW, DROP SCHEMA (dataset), and DELETE/MERGE statements with no WHERE clause."
     :name "bigquery-block-dataset-deletion"
     :description "Block DROP TABLE/VIEW, DROP SCHEMA (dataset), and DELETE/MERGE statements with no WHERE clause. Real scenario: `DROP SCHEMA analytics CASCADE;` removes a shared dataset used by dashboards."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Control costly scans"
     :card-description "Block SELECT * and unpartitioned full-table scans over very large tables that drive high on-demand query cost."
     :name "bigquery-control-costly-scans"
     :description "Block SELECT * and unpartitioned full-table scans over very large tables that drive high on-demand query cost. Real scenario: an ad-hoc `SELECT *` over a multi-TB events table burns the monthly budget."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent data exfiltration"
     :card-description "Block EXPORT DATA to external GCS buckets and large extracts of tables containing PII."
     :name "bigquery-prevent-data-exfiltration"
     :description "Block EXPORT DATA to external GCS buckets and large extracts of tables containing PII. Real scenario: customer email data is exported to a personal bucket outside the org."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "redis"
   [
    {:title "Block flush commands"
     :card-description "Block FLUSHALL and FLUSHDB on a Redis connection."
     :name "redis-block-flush-commands"
     :description "Block FLUSHALL and FLUSHDB on a Redis connection. Real scenario: `FLUSHALL` is run against prod instead of a local instance, wiping every cache and session."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard blocking keyspace ops"
     :card-description "Block O(N) and blocking commands on large datasets."
     :name "redis-guard-blocking-keyspace-ops"
     :description "Block O(N) and blocking commands on large datasets: KEYS *, unbounded SCAN loops, and FLUSH-adjacent operations. Real scenario: `KEYS *` on a multi-million-key instance stalls the event loop and times out clients."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Restrict admin and config"
     :card-description "Block CONFIG SET/REWRITE, SHUTDOWN, REPLICAOF/SLAVEOF, DEBUG, and FAILOVER."
     :name "redis-restrict-admin-and-config"
     :description "Block CONFIG SET/REWRITE, SHUTDOWN, REPLICAOF/SLAVEOF, DEBUG, and FAILOVER. Real scenario: `REPLICAOF` is run by mistake, turning the primary into a replica of a stale node."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "claude-code"
   [
    {:title "Block secret exposure"
     :card-description "Block commands that read or print credentials and secrets."
     :name "claude-code-block-secret-exposure"
     :description "Block commands that read or print credentials and secrets: cat/printenv of .env, ~/.aws/credentials, kubeconfig, or piping secret files to stdout/network. Real scenario: the agent runs `cat .env` and the secrets land in a shareable transcript."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard destructive file ops"
     :card-description "Block recursive/forced deletions and mass overwrites."
     :name "claude-code-guard-destructive-file-ops"
     :description "Block recursive/forced deletions and mass overwrites: rm -rf, find ... -delete, and git clean -fdx outside the working scope. Real scenario: an agent 'cleanup' step runs `rm -rf` one directory too high."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent untrusted execution"
     :card-description "Block piping remote scripts into a shell (curl|bash, wget|sh), installing unpinned packages, and pushing directly to protected branches/prod."
     :name "claude-code-prevent-untrusted-execution"
     :description "Block piping remote scripts into a shell (curl|bash, wget|sh), installing unpinned packages, and pushing directly to protected branches/prod. Real scenario: the agent runs `curl ... | bash` from an unverified URL."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "aws-cli"
   [
    {:title "Block resource deletion"
     :card-description "Block destructive AWS API calls."
     :name "aws-cli-block-resource-deletion"
     :description "Block destructive AWS API calls: ec2 terminate-instances, s3 rb / rm --recursive, rds delete-db-instance, and cloudformation delete-stack. Real scenario: `aws s3 rm s3://bucket --recursive` empties a production bucket."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard IAM privilege escalation"
     :card-description "Block IAM changes that grant or escalate access."
     :name "aws-cli-guard-iam-privilege-escalation"
     :description "Block IAM changes that grant or escalate access: create-user, create-access-key, attach-*-policy with AdministratorAccess, and update-assume-role-policy. Real scenario: an admin policy is attached to a build role."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent public exposure"
     :card-description "Block opening resources to the internet."
     :name "aws-cli-prevent-public-exposure"
     :description "Block opening resources to the internet: authorize-security-group-ingress with 0.0.0.0/0 on sensitive ports, put-bucket-acl public-read, and put-public-access-block disablement. Real scenario: SSH (port 22) is opened to 0.0.0.0/0 on a production security group."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "kubernetes"
   [
    {:title "Block resource deletion"
     :card-description "Block kubectl delete of namespaces, deployments, statefulsets, PVCs, and any `delete --all`."
     :name "kubernetes-block-resource-deletion"
     :description "Block kubectl delete of namespaces, deployments, statefulsets, PVCs, and any `delete --all`. Real scenario: `kubectl delete ns payments` is run against the prod context by mistake."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard privileged exec"
     :card-description "Block kubectl exec/attach into pods, port-forward to sensitive services, and applying privileged or hostPath pod specs."
     :name "kubernetes-guard-privileged-exec"
     :description "Block kubectl exec/attach into pods, port-forward to sensitive services, and applying privileged or hostPath pod specs. Real scenario: an engineer execs into a payments pod and runs shell commands directly."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Protect secret access"
     :card-description "Block reading Secret contents."
     :name "kubernetes-protect-secret-access"
     :description "Block reading Secret contents: kubectl get secret -o yaml/json and describe of secrets. Real scenario: `kubectl get secrets -o yaml` dumps base64 credentials into the session log."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "kubernetes-eks"
   [
    {:title "Block cluster changes"
     :card-description "Block deletion/scaling-to-zero of node groups, cluster deletion, and Fargate profile removal on an EKS connection."
     :name "kubernetes-eks-block-cluster-changes"
     :description "Block deletion/scaling-to-zero of node groups, cluster deletion, and Fargate profile removal on an EKS connection. Real scenario: a node group is scaled to 0, evicting all production workloads."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard RBAC and aws-auth"
     :card-description "Block edits to the aws-auth ConfigMap, ClusterRoleBinding changes, and binding of cluster-admin."
     :name "kubernetes-eks-guard-rbac-and-authmap"
     :description "Block edits to the aws-auth ConfigMap, ClusterRoleBinding changes, and binding of cluster-admin. Real scenario: an IAM role is mapped to system:masters in aws-auth, granting cluster-wide admin."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Protect secret exfiltration"
     :card-description "Block reading and decoding Secrets (get secret -o yaml, base64 -d pipelines) on the EKS cluster."
     :name "kubernetes-eks-protect-secret-exfiltration"
     :description "Block reading and decoding Secrets (get secret -o yaml, base64 -d pipelines) on the EKS cluster. Real scenario: database credentials are pulled from a Secret and decoded mid-session."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "git"
   [
    {:title "Block history rewrite"
     :card-description "Block force pushes (push --force / -f) to protected branches, reset --hard, and filter-branch/filter-repo."
     :name "git-block-history-rewrite"
     :description "Block force pushes (push --force / -f) to protected branches, reset --hard, and filter-branch/filter-repo. Real scenario: `git push --force` to main overwrites a teammate's merged commits."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard branch and tag deletion"
     :card-description "Block deletion of remote branches/tags (push --delete, push :branch) and `branch -D` of protected branches."
     :name "git-guard-branch-and-tag-deletion"
     :description "Block deletion of remote branches/tags (push --delete, push :branch) and `branch -D` of protected branches. Real scenario: a release tag is deleted, breaking the deploy pipeline's version reference."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent secret commits"
     :card-description "Block staging/committing of credential files and large binaries."
     :name "git-prevent-secret-commits"
     :description "Block staging/committing of credential files and large binaries: add of .env, *.pem, id_rsa, and service-account keys. Real scenario: a developer commits a private key into the repo history."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "github"
   [
    {:title "Block repo deletion"
     :card-description "Block destructive repo operations via gh CLI."
     :name "github-block-repo-deletion"
     :description "Block destructive repo operations via gh CLI: repo delete, repo archive, and bulk issue/PR deletion. Real scenario: `gh repo delete org/payments` is run while context-switching between repos."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard branch protection changes"
     :card-description "Block disabling branch protection, required reviews, or status checks via gh api calls."
     :name "github-guard-branch-protection-changes"
     :description "Block disabling branch protection, required reviews, or status checks via gh api calls. Real scenario: required reviews are turned off on main to push a hotfix unreviewed."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent visibility and secret exposure"
     :card-description "Block making private repos public, printing repo/org secrets, and rotating tokens with broad scope."
     :name "github-prevent-visibility-and-secret-exposure"
     :description "Block making private repos public, printing repo/org secrets, and rotating tokens with broad scope. Real scenario: `gh repo edit --visibility public` exposes a private codebase."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "web-application"
   [
    {:title "Block destructive HTTP"
     :card-description "Block destructive HTTP requests through the proxy."
     :name "web-application-block-destructive-http"
     :description "Block destructive HTTP requests through the proxy: DELETE/PUT to admin or resource endpoints and bulk-delete API routes. Real scenario: a DELETE to `/api/v1/users` removes accounts via an internal admin API."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard admin endpoint access"
     :card-description "Block access to administrative and config routes (/admin, /internal, feature-flag and user-management endpoints)."
     :name "web-application-guard-admin-endpoint-access"
     :description "Block access to administrative and config routes (/admin, /internal, feature-flag and user-management endpoints). Real scenario: an engineer toggles a production feature flag through the admin panel without review."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent bulk data export"
     :card-description "Block requests to bulk-export or PII endpoints (export/report/download APIs) returning large customer datasets."
     :name "web-application-prevent-bulk-data-export"
     :description "Block requests to bulk-export or PII endpoints (export/report/download APIs) returning large customer datasets. Real scenario: a full customer export endpoint is hit and downloaded during a 'quick check'."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "ruby-on-rails"
   [
    {:title "Block mass destruction"
     :card-description "Block destructive ActiveRecord calls in the Rails console."
     :name "ruby-on-rails-block-mass-destruction"
     :description "Block destructive ActiveRecord calls in the Rails console: Model.destroy_all, delete_all, and connection-level DROP/TRUNCATE. Real scenario: `User.delete_all` is run to 'reset' data on production."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard user and credential changes"
     :card-description "Block mass user/role/password mutations."
     :name "ruby-on-rails-guard-user-and-credential-changes"
     :description "Block mass user/role/password mutations: User.update_all on roles or passwords and admin-flag flips. Real scenario: `User.update_all(admin: true)` accidentally promotes every account."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent raw SQL and shell"
     :card-description "Block raw SQL via ActiveRecord::Base.connection.execute and OS calls (system, exec, backticks, %x)."
     :name "ruby-on-rails-prevent-raw-sql-and-shell"
     :description "Block raw SQL via ActiveRecord::Base.connection.execute and OS calls (system, exec, backticks, %x). Real scenario: a raw `connection.execute('DELETE FROM payments')` bypasses model safeguards."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "python"
   [
    {:title "Block filesystem and system ops"
     :card-description "Block destructive OS calls in the Python REPL."
     :name "python-block-filesystem-and-system-ops"
     :description "Block destructive OS calls in the Python REPL: os.system/subprocess with rm -rf, shutil.rmtree, and os.remove loops over important paths. Real scenario: `shutil.rmtree('/data')` deletes a mounted volume."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard network exfiltration"
     :card-description "Block outbound requests that upload local data to external hosts (requests.post/urllib with file payloads, raw sockets)."
     :name "python-guard-network-exfiltration"
     :description "Block outbound requests that upload local data to external hosts (requests.post/urllib with file payloads, raw sockets). Real scenario: a script reads a credentials file and POSTs it to an external URL."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent secret access"
     :card-description "Block reading of secrets and credentials."
     :name "python-prevent-secret-access"
     :description "Block reading of secrets and credentials: os.environ dumps of secret keys, reading .env / ~/.aws/credentials, and printing API keys. Real scenario: `print(os.environ)` leaks every secret into the session output."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "php-artisan"
   [
    {:title "Block migrate wipe"
     :card-description "Block destructive Artisan commands."
     :name "php-artisan-block-migrate-wipe"
     :description "Block destructive Artisan commands: migrate:fresh, migrate:reset, db:wipe, and migrate:rollback against production. Real scenario: `php artisan migrate:fresh` drops and rebuilds every table on prod."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard destructive tinker"
     :card-description "Block destructive Eloquent operations in tinker."
     :name "php-artisan-guard-destructive-tinker"
     :description "Block destructive Eloquent operations in tinker: Model::truncate(), ::query()->delete() with no constraints, and DB::statement DROP/TRUNCATE. Real scenario: `User::truncate()` is run in tinker on the live database."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Restrict key and config"
     :card-description "Block key:generate (rotates APP_KEY and invalidates sessions/encrypted data), config/cache clears in prod, and queue:flush."
     :name "php-artisan-restrict-key-and-config"
     :description "Block key:generate (rotates APP_KEY and invalidates sessions/encrypted data), config/cache clears in prod, and queue:flush. Real scenario: `php artisan key:generate` rotates the app key and breaks decryption."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "elixir"
   [
    {:title "Block node and process kills"
     :card-description "Block VM/node shutdown and forced process kills in IEx."
     :name "elixir-block-node-and-process-kills"
     :description "Block VM/node shutdown and forced process kills in IEx: System.halt, :init.stop, and Process.exit on supervisors. Real scenario: `System.halt()` is typed in a prod node and takes the service down."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard repo destruction"
     :card-description "Block mass Ecto operations."
     :name "elixir-guard-repo-destruction"
     :description "Block mass Ecto operations: Repo.delete_all/update_all without constraints and raw destructive SQL via Ecto.Adapters.SQL.query. Real scenario: `Repo.delete_all(Account)` clears the accounts table."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent OS command execution"
     :card-description "Block OS command execution from IEx."
     :name "elixir-prevent-os-command-execution"
     :description "Block OS command execution from IEx: System.cmd and :os.cmd. Real scenario: `System.cmd(\"rm\", [\"-rf\", \"/app/uploads\"])` is run from the remote console."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "clojure"
   [
    {:title "Block shell and system"
     :card-description "Block shell execution and VM exit in the REPL."
     :name "clojure-block-shell-and-system"
     :description "Block shell execution and VM exit in the REPL: clojure.java.shell/sh, (System/exit), and (.exec (Runtime/getRuntime)). Real scenario: a `sh \"rm\" \"-rf\" ...` call deletes app data from the REPL."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard destructive DB ops"
     :card-description "Block mass mutations through clojure.java.jdbc / next.jdbc."
     :name "clojure-guard-destructive-db-ops"
     :description "Block mass mutations through clojure.java.jdbc / next.jdbc: delete!/execute! of DELETE or TRUNCATE with no predicate. Real scenario: an unfiltered `execute! [\"DELETE FROM sessions\"]` clears active sessions."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "allow_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent untrusted eval"
     :card-description "Block evaluation of remote/untrusted code."
     :name "clojure-prevent-untrusted-eval"
     :description "Block evaluation of remote/untrusted code: load-string/eval/read-string of fetched content and slurp of external URLs into eval. Real scenario: `(eval (read-string (slurp url)))` runs unverified code in prod."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "nodejs"
   [
    {:title "Block filesystem destruction"
     :card-description "Block destructive fs operations in the Node REPL."
     :name "nodejs-block-filesystem-destruction"
     :description "Block destructive fs operations in the Node REPL: fs.rmSync/rm with recursive:true and fs.unlink loops over critical paths. Real scenario: `fs.rmSync('/srv/data', {recursive:true})` deletes a production directory."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard process and shell"
     :card-description "Block child_process exec/execSync/spawn of shell commands and process.exit in a prod console."
     :name "nodejs-guard-process-and-shell"
     :description "Block child_process exec/execSync/spawn of shell commands and process.exit in a prod console. Real scenario: `child_process.execSync('rm -rf node_modules /app')` is run from the REPL."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent secret exfiltration"
     :card-description "Block printing of process.env secrets and outbound uploads of local files (http/https requests with file bodies)."
     :name "nodejs-prevent-secret-exfiltration"
     :description "Block printing of process.env secrets and outbound uploads of local files (http/https requests with file bodies). Real scenario: `console.log(process.env)` dumps every secret into the transcript."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "ssh"
   [
    {:title "Block destructive shell"
     :card-description "Block catastrophic shell commands on an SSH connection."
     :name "ssh-block-destructive-shell"
     :description "Block catastrophic shell commands on an SSH connection: rm -rf / (and root-level paths), mkfs, dd to a block device, and `> /dev/sda`. Real scenario: `rm -rf /` (or a stray space in a variable path) wipes the server filesystem."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard privilege escalation"
     :card-description "Block privilege escalation and permission tampering."
     :name "ssh-guard-privilege-escalation"
     :description "Block privilege escalation and permission tampering: sudo su -, chmod 777 on system paths, and edits to /etc/sudoers, /etc/passwd, or /etc/shadow. Real scenario: a user adds themselves to sudoers for persistent root."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent service and firewall tampering"
     :card-description "Block stopping/disabling critical services and security controls."
     :name "ssh-prevent-service-and-firewall-tampering"
     :description "Block stopping/disabling critical services and security controls: systemctl stop/disable of core services, iptables -F / ufw disable, and stopping auditd. Real scenario: the firewall is flushed 'to test connectivity' and left open."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]

   "rdp"
   [
    {:title "Block system config changes"
     :card-description "Block high-impact Windows configuration changes during an RDP session."
     :name "rdp-block-system-config-changes"
     :description "Block high-impact Windows configuration changes during an RDP session: disabling Windows Defender or the firewall, registry edits to security keys, and Group Policy changes. Real scenario: Defender real-time protection is turned off to run an unverified tool."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Guard account management"
     :card-description "Block local/domain account changes."
     :name "rdp-guard-account-management"
     :description "Block local/domain account changes: creating local admins (net user / net localgroup administrators add) and password resets for other users. Real scenario: a new local administrator account is created and left behind."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}

    {:title "Prevent data exfiltration and disruption"
     :card-description "Block exfiltration and service disruption."
     :name "rdp-prevent-data-exfiltration-and-disruption"
     :description "Block exfiltration and service disruption: copying sensitive files to mapped/clipboard drives and stopping critical Windows services. Real scenario: a database service is stopped and a data folder copied to a redirected drive."
     :connection_names []
     :risk_evaluation {:low_risk {:action "allow_execution"}
                       :medium_risk {:action "block_execution"}
                       :high_risk {:action "block_execution"}}}
]})

(defn for-subtype
  "Returns the curated list of templates for the given connection subtype, or [] if none."
  [subtype]
  (get templates-by-subtype subtype []))

(defn find-by-name
  "Flat lookup of a template by its :name across all subtypes. Template names
  are globally unique and double as deep-link ids (?template=)."
  [template-name]
  (->> (vals templates-by-subtype)
       (apply concat)
       (some #(when (= (:name %) template-name) %))))

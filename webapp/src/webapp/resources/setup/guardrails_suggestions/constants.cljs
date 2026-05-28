(ns webapp.resources.setup.guardrails-suggestions.constants)

;; Curated guardrail suggestions, indexed by connection subtype.
;; Each entry already conforms to the POST /guardrails payload shape
;; (name, description, subtype, attributes, connection_ids, input.rules, output.rules).
;; The :title and :card-description fields are UI-only labels for the suggestion card.
(def suggestions-by-subtype
  {"postgres"
   [{:title "Reinforce WHERE clause"
     :card-description "Blocks UPDATE and DELETE without WHERE clause"
     :name "pg-block-unsafe-write"
     :description "PostgreSQL: block UPDATE/DELETE without WHERE"
     :subtype "postgres"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}
     :output {:rules []}}
    {:title "Block destructive DDL"
     :card-description "Blocks words like DROP TABLE, TRUNCATE, DROP DATABASE, DROP SCHEMA"
     :name "pg-block-destructive-ddl"
     :description "PostgreSQL: block DROP/TRUNCATE on tables, schemas, databases"
     :subtype "postgres"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["DROP TABLE" "TRUNCATE" "DROP DATABASE" "DROP SCHEMA"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block pgcrypto output"
     :card-description "Blocks filesystem-access function results"
     :name "pg-mask-pgcrypto-output"
     :description "PostgreSQL: redact filesystem-access function output"
     :subtype "postgres"
     :attributes []
     :connection_ids []
     :input {:rules []}
     :output {:rules [{:type "pattern_match"
                       :words []
                       :pattern_regex "(?i)\\b(pg_read_file|pg_ls_dir|pg_stat_file)\\b"}]}}]

   "mysql"
   [{:title "Reinforce WHERE clause"
     :card-description "Blocks UPDATE and DELETE without WHERE clause"
     :name "mysql-block-unsafe-write"
     :description "MySQL: block UPDATE/DELETE without WHERE"
     :subtype "mysql"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}
     :output {:rules []}}
    {:title "Block GRANT/REVOKE changes"
     :card-description "Blocks privilege and authentication changes"
     :name "mysql-block-grant-changes"
     :description "MySQL: block privilege and authentication changes"
     :subtype "mysql"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["GRANT" "REVOKE" "CREATE USER" "DROP USER" "SET PASSWORD"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block LOAD INFILE / OUTFILE"
     :card-description "Blocks filesystem read/write via SQL"
     :name "mysql-block-load-infile"
     :description "MySQL: block filesystem read/write via SQL"
     :subtype "mysql"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bLOAD\\s+DATA\\b|\\bINTO\\s+OUTFILE\\b|\\bINTO\\s+DUMPFILE\\b"}]}
     :output {:rules []}}]

   "mongodb"
   [{:title "Block destructive drops"
     :card-description "Blocks collection and database drops"
     :name "mongo-block-drop"
     :description "MongoDB: block collection and database drops"
     :subtype "mongodb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["dropDatabase" "db.dropDatabase" ".drop(" "dropAllUsers"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block unfiltered writes"
     :card-description "Blocks updateMany/deleteMany with empty filter"
     :name "mongo-block-unfiltered-write"
     :description "MongoDB: block updateMany/deleteMany with empty filter"
     :subtype "mongodb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\.(updateMany|deleteMany)\\s*\\(\\s*\\{\\s*\\}"}]}
     :output {:rules []}}
    {:title "Block server-side eval"
     :card-description "Blocks server-side JavaScript execution"
     :name "mongo-block-eval"
     :description "MongoDB: block server-side JavaScript execution"
     :subtype "mongodb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["db.eval" "$where" "mapReduce"]
                      :pattern_regex ""}]}
     :output {:rules []}}]

   "dynamodb"
   [{:title "Block table deletion"
     :card-description "Blocks table deletion API calls"
     :name "ddb-block-table-delete"
     :description "DynamoDB: block table deletion API calls"
     :subtype "dynamodb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["DeleteTable" "delete-table"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block full Scan"
     :card-description "Blocks Scan without FilterExpression"
     :name "ddb-block-full-scan"
     :description "DynamoDB: block Scan without FilterExpression"
     :subtype "dynamodb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bScan\\b(?!.*\\bFilterExpression\\b)"}]}
     :output {:rules []}}
    {:title "Block batch purge"
     :card-description "Blocks bulk deletes via BatchWriteItem"
     :name "ddb-block-batch-purge"
     :description "DynamoDB: block bulk deletes via BatchWriteItem"
     :subtype "dynamodb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)BatchWriteItem.*DeleteRequest"}]}
     :output {:rules []}}]

   "oracledb"
   [{:title "Reinforce WHERE clause"
     :card-description "Blocks UPDATE and DELETE without WHERE clause"
     :name "oracle-block-unsafe-write"
     :description "Oracle: block UPDATE/DELETE without WHERE"
     :subtype "oracledb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}
     :output {:rules []}}
    {:title "Block instance/system control"
     :card-description "Blocks SHUTDOWN, STARTUP, ALTER SYSTEM, ALTER DATABASE"
     :name "oracle-block-shutdown"
     :description "Oracle: block instance/system control statements"
     :subtype "oracledb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["SHUTDOWN" "STARTUP" "ALTER SYSTEM" "ALTER DATABASE"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block UTL/DBMS packages"
     :card-description "Blocks filesystem and network UTL/DBMS packages"
     :name "oracle-block-utl-packages"
     :description "Oracle: block filesystem and network UTL/DBMS packages"
     :subtype "oracledb"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(UTL_FILE|UTL_HTTP|UTL_SMTP|DBMS_LOB\\.LOADFROMFILE)\\b"}]}
     :output {:rules []}}]

   "mssql"
   [{:title "Reinforce WHERE clause"
     :card-description "Blocks UPDATE and DELETE without WHERE clause"
     :name "mssql-block-unsafe-write"
     :description "MSSQL: block UPDATE/DELETE without WHERE"
     :subtype "mssql"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}
     :output {:rules []}}
    {:title "Block xp_cmdshell"
     :card-description "Blocks xp_cmdshell and OLE automation procs"
     :name "mssql-block-xp-cmdshell"
     :description "MSSQL: block xp_cmdshell and OLE automation procs"
     :subtype "mssql"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["xp_cmdshell" "sp_configure" "EXEC sp_OACreate"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block bulk I/O"
     :card-description "Blocks BULK INSERT and OPENROWSET I/O"
     :name "mssql-block-bulk-io"
     :description "MSSQL: block BULK INSERT and OPENROWSET I/O"
     :subtype "mssql"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bBULK\\s+INSERT\\b|\\bOPENROWSET\\b|\\bOPENDATASOURCE\\b"}]}
     :output {:rules []}}]

   "cassandra"
   [{:title "Block TRUNCATE / DROP"
     :card-description "Blocks TRUNCATE and DROP statements"
     :name "cassandra-block-truncate"
     :description "Cassandra: block TRUNCATE and DROP statements"
     :subtype "cassandra"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["TRUNCATE" "DROP KEYSPACE" "DROP TABLE"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block ALLOW FILTERING"
     :card-description "Blocks ALLOW FILTERING (full-cluster scan)"
     :name "cassandra-block-allow-filtering"
     :description "Cassandra: block ALLOW FILTERING (full-cluster scan)"
     :subtype "cassandra"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bALLOW\\s+FILTERING\\b"}]}
     :output {:rules []}}
    {:title "Block UNLOGGED BATCH"
     :card-description "Blocks UNLOGGED BATCH (consistency risk)"
     :name "cassandra-block-unlogged-batch"
     :description "Cassandra: block UNLOGGED BATCH (consistency risk)"
     :subtype "cassandra"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bBEGIN\\s+UNLOGGED\\s+BATCH\\b"}]}
     :output {:rules []}}]

   "bigquery"
   [{:title "Block DROP / TRUNCATE"
     :card-description "Blocks dataset/table removal"
     :name "bq-block-drop"
     :description "BigQuery: block dataset/table removal"
     :subtype "bigquery"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["DROP TABLE" "DROP SCHEMA" "DROP DATASET" "TRUNCATE TABLE"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Require LIMIT"
     :card-description "Blocks unbounded SELECT (cost control)"
     :name "bq-require-limit"
     :description "BigQuery: block unbounded SELECT (cost control)"
     :subtype "bigquery"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)^\\s*SELECT\\b(?!.*\\bLIMIT\\b)"}]}
     :output {:rules []}}
    {:title "Block EXPORT DATA"
     :card-description "Blocks EXPORT DATA and external table creation"
     :name "bq-block-export"
     :description "BigQuery: block EXPORT DATA and external table creation"
     :subtype "bigquery"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bEXPORT\\s+DATA\\b|\\bCREATE\\s+EXTERNAL\\s+TABLE\\b"}]}
     :output {:rules []}}]

   "redis"
   [{:title "Block FLUSH / SHUTDOWN"
     :card-description "Blocks flush, debug, and shutdown commands"
     :name "redis-block-flush"
     :description "Redis: block flush, debug, and shutdown commands"
     :subtype "redis"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["FLUSHALL" "FLUSHDB" "DEBUG" "SHUTDOWN"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block CONFIG / ACL changes"
     :card-description "Blocks live CONFIG and ACL changes"
     :name "redis-block-config"
     :description "Redis: block live CONFIG and ACL changes"
     :subtype "redis"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)^\\s*(CONFIG|ACL)\\s+(SET|RESETSTAT|REWRITE|DELUSER)"}]}
     :output {:rules []}}
    {:title "Block KEYS *"
     :card-description "Blocks blocking KEYS * scan"
     :name "redis-block-keys-scan-all"
     :description "Redis: block blocking KEYS * scan"
     :subtype "redis"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)^\\s*KEYS\\s+\\*\\s*$"}]}
     :output {:rules []}}]

   "claude-code"
   [{:title "Block destructive shell"
     :card-description "Blocks destructive/piped-shell prompt patterns"
     :name "claude-block-shell-exec"
     :description "Claude Code: block destructive/piped-shell prompt patterns"
     :subtype "claude-code"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(rm\\s+-rf\\s+/|sudo\\s+|curl\\s+.*\\|\\s*sh|wget\\s+.*\\|\\s*sh)\\b"}]}
     :output {:rules []}}
    {:title "Mask PII in output"
     :card-description "Redacts SSN/email/PAN in model output"
     :name "claude-mask-pii-output"
     :description "Claude Code: redact SSN/email/PAN in model output"
     :subtype "claude-code"
     :attributes []
     :connection_ids []
     :input {:rules []}
     :output {:rules [{:type "pattern_match"
                       :words []
                       :pattern_regex "(?i)\\b(\\d{3}-\\d{2}-\\d{4}|[\\w.+-]+@[\\w-]+\\.[\\w.-]+|\\d{13,19})\\b"}]}}
    {:title "Block prompt injection"
     :card-description "Blocks common prompt-injection phrases"
     :name "claude-block-prompt-injection"
     :description "Claude Code: block common prompt-injection phrases"
     :subtype "claude-code"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["ignore previous instructions"
                              "disregard the system prompt"
                              "you are now"
                              "developer mode"]
                      :pattern_regex ""}]}
     :output {:rules []}}]

   "aws-cli"
   [{:title "Block IAM mutations"
     :card-description "Blocks IAM identity/policy mutations"
     :name "aws-block-iam-mutations"
     :description "AWS CLI: block IAM identity/policy mutations"
     :subtype "aws-cli"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(iam\\s+(create|delete|attach|put)-(user|policy|role|access-key))\\b"}]}
     :output {:rules []}}
    {:title "Block account destruction"
     :card-description "Blocks resource teardown commands"
     :name "aws-block-account-destruction"
     :description "AWS CLI: block resource teardown commands"
     :subtype "aws-cli"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["delete-bucket"
                              "terminate-instances"
                              "delete-db-instance"
                              "delete-cluster"
                              "delete-stack"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Mask credentials in output"
     :card-description "Redacts access keys in output"
     :name "aws-mask-credentials-output"
     :description "AWS CLI: redact access keys in output"
     :subtype "aws-cli"
     :attributes []
     :connection_ids []
     :input {:rules []}
     :output {:rules [{:type "pattern_match"
                       :words []
                       :pattern_regex "(?i)\\b(AKIA[0-9A-Z]{16}|aws_secret_access_key\\s*=\\s*\\S+)\\b"}]}}]

   "kubernetes"
   [{:title "Block destructive deletes"
     :card-description "Blocks cluster-wide deletes"
     :name "k8s-block-destructive"
     :description "Kubernetes: block cluster-wide deletes"
     :subtype "kubernetes"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bkubectl\\s+delete\\s+(ns|namespace|all|crd|nodes?)\\b"}]}
     :output {:rules []}}
    {:title "Block privileged exec"
     :card-description "Blocks interactive shells into pods"
     :name "k8s-block-privileged-exec"
     :description "Kubernetes: block interactive shells into pods"
     :subtype "kubernetes"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bkubectl\\s+exec\\b.*--\\s*(sh|bash)\\b"}]}
     :output {:rules []}}
    {:title "Mask Secret output"
     :card-description "Redacts Secret data fields in output"
     :name "k8s-mask-secret-output"
     :description "Kubernetes: redact Secret data fields in output"
     :subtype "kubernetes"
     :attributes []
     :connection_ids []
     :input {:rules []}
     :output {:rules [{:type "pattern_match"
                       :words []
                       :pattern_regex "(?i)\"(data|stringData)\":\\s*\\{[^}]*\\}"}]}}]

   "kubernetes-eks"
   [{:title "Block cluster mutations"
     :card-description "Blocks cluster lifecycle changes"
     :name "eks-block-cluster-mutate"
     :description "EKS: block cluster lifecycle changes"
     :subtype "kubernetes-eks"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["eks delete-cluster"
                              "eks delete-nodegroup"
                              "eks update-cluster-config"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block aws-auth edits"
     :card-description "Blocks edits to aws-auth ConfigMap"
     :name "eks-block-aws-auth-edit"
     :description "EKS: block edits to aws-auth ConfigMap"
     :subtype "kubernetes-eks"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bkubectl\\s+(edit|apply|replace)\\b.*\\baws-auth\\b"}]}
     :output {:rules []}}
    {:title "Block IAM bindings"
     :card-description "Blocks ClusterRoleBinding creation (escalation)"
     :name "eks-block-iam-binding"
     :description "EKS: block ClusterRoleBinding creation (escalation)"
     :subtype "kubernetes-eks"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bkubectl\\s+(create|apply)\\s+.*clusterrolebinding\\b"}]}
     :output {:rules []}}]

   "git"
   [{:title "Block force push"
     :card-description "Blocks force push (history rewrite)"
     :name "git-block-force-push"
     :description "Git: block force push (history rewrite)"
     :subtype "git"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bgit\\s+push\\s+(--force|-f)\\b"}]}
     :output {:rules []}}
    {:title "Block history rewrite"
     :card-description "Blocks history-rewriting tooling"
     :name "git-block-history-rewrite"
     :description "Git: block history-rewriting tooling"
     :subtype "git"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["git filter-branch"
                              "git filter-repo"
                              "git reset --hard origin"
                              "git push --mirror"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block credential helper tampering"
     :card-description "Blocks credential helper tampering"
     :name "git-block-credential-leak"
     :description "Git: block credential helper tampering"
     :subtype "git"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bgit\\s+config\\s+.*\\b(credential\\.helper|http\\..*\\.extraheader)\\b"}]}
     :output {:rules []}}]

   "github"
   [{:title "Block repo deletion"
     :card-description "Blocks repo deletion and archival"
     :name "gh-block-repo-delete"
     :description "GitHub: block repo deletion and archival"
     :subtype "github"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["gh repo delete"
                              "gh repo archive"
                              "repos/.+ -X DELETE"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block secret mutations"
     :card-description "Blocks Actions secrets changes"
     :name "gh-block-secret-mutation"
     :description "GitHub: block Actions secrets changes"
     :subtype "github"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bgh\\s+secret\\s+(set|delete)\\b"}]}
     :output {:rules []}}
    {:title "Block admin perms changes"
     :card-description "Blocks org/team membership API mutations"
     :name "gh-block-admin-perms"
     :description "GitHub: block org/team membership API mutations"
     :subtype "github"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bgh\\s+api\\b.*/(orgs|teams)/.*\\b(members|admins)\\b.*-X\\s+(PUT|DELETE)"}]}
     :output {:rules []}}]

   "web-application"
   [{:title "Block destructive verbs"
     :card-description "Blocks DELETE/PURGE requests"
     :name "http-block-destructive-verbs"
     :description "HTTP Proxy: block DELETE/PURGE requests"
     :subtype "web-application"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)^(DELETE|PURGE)\\s+/"}]}
     :output {:rules []}}
    {:title "Block admin paths"
     :card-description "Blocks admin/debug endpoint paths"
     :name "http-block-admin-paths"
     :description "HTTP Proxy: block admin/debug endpoint paths"
     :subtype "web-application"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\s/(admin|internal|debug|actuator)(/|\\s)"}]}
     :output {:rules []}}
    {:title "Mask token output"
     :card-description "Redacts bearer tokens and API keys in responses"
     :name "http-mask-token-output"
     :description "HTTP Proxy: redact bearer tokens and API keys in responses"
     :subtype "web-application"
     :attributes []
     :connection_ids []
     :input {:rules []}
     :output {:rules [{:type "pattern_match"
                       :words []
                       :pattern_regex "(?i)(authorization:\\s*bearer\\s+\\S+|\"(api[_-]?key|access_token)\"\\s*:\\s*\"[^\"]+\")"}]}}]

   "ruby-on-rails"
   [{:title "Block destroy_all"
     :card-description "Blocks mass-mutation methods"
     :name "rails-block-destroy-all"
     :description "Rails console: block mass-mutation methods"
     :subtype "ruby-on-rails"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\.(destroy_all|delete_all|update_all)\\b"}]}
     :output {:rules []}}
    {:title "Block shell exec"
     :card-description "Blocks shell-out from console"
     :name "rails-block-shell-exec"
     :description "Rails console: block shell-out from console"
     :subtype "ruby-on-rails"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)(\\bsystem\\(|`[^`]+`|%x\\{|Kernel\\.exec)"}]}
     :output {:rules []}}
    {:title "Block credentials read"
     :card-description "Blocks credential and secret reads"
     :name "rails-block-credentials"
     :description "Rails console: block credential and secret reads"
     :subtype "ruby-on-rails"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["Rails.application.credentials"
                              "ENV['SECRET_KEY_BASE']"
                              "ActiveSupport::MessageEncryptor"]
                      :pattern_regex ""}]}
     :output {:rules []}}]

   "python"
   [{:title "Block shell exec"
     :card-description "Blocks shell exec"
     :name "py-block-shell"
     :description "Python REPL: block shell exec"
     :subtype "python"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(os\\.system|subprocess\\.|os\\.popen|pty\\.spawn)\\b"}]}
     :output {:rules []}}
    {:title "Block network exfil"
     :card-description "Blocks outbound HTTP and socket connections"
     :name "py-block-network-exfil"
     :description "Python REPL: block outbound HTTP and socket connections"
     :subtype "python"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(requests\\.(post|put)|urllib\\.request\\.urlopen|socket\\.connect)\\b"}]}
     :output {:rules []}}
    {:title "Block dynamic eval"
     :card-description "Blocks dynamic code execution"
     :name "py-block-eval"
     :description "Python REPL: block dynamic code execution"
     :subtype "python"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["eval(" "exec(" "compile(" "__import__("]
                      :pattern_regex ""}]}
     :output {:rules []}}]

   "php-artisan"
   [{:title "Block migrate:fresh"
     :card-description "Blocks schema-wipe migrations"
     :name "artisan-block-migrate-fresh"
     :description "Artisan: block schema-wipe migrations"
     :subtype "php-artisan"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["migrate:fresh"
                              "migrate:reset"
                              "db:wipe"
                              "migrate:rollback --force"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block tinker exec"
     :card-description "Blocks PHP shell exec functions"
     :name "artisan-block-tinker-exec"
     :description "Artisan Tinker: block PHP shell exec functions"
     :subtype "php-artisan"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(shell_exec|exec\\(|passthru\\(|proc_open\\()\\b"}]}
     :output {:rules []}}
    {:title "Block key:generate"
     :card-description "Blocks app-key and env mutations"
     :name "artisan-block-key-generate"
     :description "Artisan: block app-key and env mutations"
     :subtype "php-artisan"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["key:generate" "config:cache --force" "env:decrypt"]
                      :pattern_regex ""}]}
     :output {:rules []}}]

   "elixir"
   [{:title "Block System.cmd"
     :card-description "Blocks OS command execution"
     :name "iex-block-system-cmd"
     :description "IEx: block OS command execution"
     :subtype "elixir"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(System\\.cmd|:os\\.cmd|Port\\.open)\\b"}]}
     :output {:rules []}}
    {:title "Block node control"
     :card-description "Blocks node and application shutdown"
     :name "iex-block-node-control"
     :description "IEx: block node and application shutdown"
     :subtype "elixir"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["Node.stop"
                              ":init.stop"
                              ":erlang.halt"
                              "Application.stop"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block Repo mass-mutations"
     :card-description "Blocks mass Ecto mutations"
     :name "iex-block-repo-truncate"
     :description "IEx: block mass Ecto mutations"
     :subtype "elixir"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bRepo\\.(delete_all|update_all)\\b"}]}
     :output {:rules []}}]

   "clojure"
   [{:title "Block clojure.java.shell"
     :card-description "Blocks clojure.java.shell calls"
     :name "clj-block-shell"
     :description "Clojure REPL: block clojure.java.shell calls"
     :subtype "clojure"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(clojure\\.java\\.shell/sh|sh/sh|\\(shell/sh\\b)"}]}
     :output {:rules []}}
    {:title "Block dynamic load"
     :card-description "Blocks dynamic code loading"
     :name "clj-block-eval-load"
     :description "Clojure REPL: block dynamic code loading"
     :subtype "clojure"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["load-string" "load-file" "eval" "clojure.core/load"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block System/exit"
     :card-description "Blocks JVM shutdown"
     :name "clj-block-system-exit"
     :description "Clojure REPL: block JVM shutdown"
     :subtype "clojure"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\(System/exit|\\(\\.exit\\s+\\(Runtime/getRuntime\\)"}]}
     :output {:rules []}}]

   "nodejs"
   [{:title "Block child_process"
     :card-description "Blocks child_process spawns"
     :name "node-block-child-process"
     :description "Node REPL: block child_process spawns"
     :subtype "nodejs"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(child_process|require\\(['\"]child_process['\"]\\))"}]}
     :output {:rules []}}
    {:title "Block fs writes"
     :card-description "Blocks filesystem mutations"
     :name "node-block-fs-write"
     :description "Node REPL: block filesystem mutations"
     :subtype "nodejs"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\bfs\\.(unlink|rm|rmdir|writeFile)(Sync)?\\b"}]}
     :output {:rules []}}
    {:title "Block dynamic eval"
     :card-description "Blocks dynamic code execution"
     :name "node-block-eval"
     :description "Node REPL: block dynamic code execution"
     :subtype "nodejs"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["eval("
                              "Function("
                              "vm.runInThisContext"
                              "vm.runInNewContext"]
                      :pattern_regex ""}]}
     :output {:rules []}}]

   "ssh"
   [{:title "Block rm -rf"
     :card-description "Blocks recursive root/home deletes"
     :name "ssh-block-rm-rf"
     :description "SSH: block recursive root/home deletes"
     :subtype "ssh"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\brm\\s+(-[rRf]+\\s+)+(/|/\\*|\\$HOME|~)"}]}
     :output {:rules []}}
    {:title "Block privilege escalation"
     :card-description "Blocks privilege escalation"
     :name "ssh-block-priv-esc"
     :description "SSH: block privilege escalation"
     :subtype "ssh"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["sudo su"
                              "sudo -i"
                              "chmod 777 /"
                              "passwd root"
                              "usermod -aG sudo"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block pipe-to-shell"
     :card-description "Blocks curl|sh and wget|sh install patterns"
     :name "ssh-block-pipe-to-shell"
     :description "SSH: block curl|sh and wget|sh install patterns"
     :subtype "ssh"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(curl|wget)\\b[^|]*\\|\\s*(sh|bash|zsh)\\b"}]}
     :output {:rules []}}]

   "rdp"
   [{:title "Block format disk"
     :card-description "Blocks disk wipe commands"
     :name "rdp-block-format-disk"
     :description "RDP: block disk wipe commands"
     :subtype "rdp"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["format C:"
                              "diskpart"
                              "cipher /w"
                              "Remove-Item -Recurse C:\\"]
                      :pattern_regex ""}]}
     :output {:rules []}}
    {:title "Block user mgmt"
     :card-description "Blocks local user/admin changes"
     :name "rdp-block-user-mgmt"
     :description "RDP: block local user/admin changes"
     :subtype "rdp"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "pattern_match"
                      :words []
                      :pattern_regex "(?i)\\b(net\\s+user\\s+\\S+\\s+/(add|delete)|net\\s+localgroup\\s+administrators\\b)"}]}
     :output {:rules []}}
    {:title "Block defender disable"
     :card-description "Blocks Defender/security control bypass"
     :name "rdp-block-defender-disable"
     :description "RDP: block Defender/security control bypass"
     :subtype "rdp"
     :attributes []
     :connection_ids []
     :input {:rules [{:type "deny_words_list"
                      :words ["Set-MpPreference -DisableRealtimeMonitoring"
                              "sc stop WinDefend"
                              "bcdedit /set testsigning on"]
                      :pattern_regex ""}]}
     :output {:rules []}}]})

(defn for-subtype
  "Returns the curated list of suggestions for the given connection subtype, or [] if none."
  [subtype]
  (get suggestions-by-subtype subtype []))

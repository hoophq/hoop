package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
)

const defaultRulepackVersion = "0.1.0"

// defaultRulepackSpec describes one managed rulepack plus its guardrail rules.
// The data here is the single source of truth used by SeedDefaultRulepacksForOrg
// and must stay in sync with the SQL migration that backfills existing orgs
// (rootfs/app/migrations/000093_default_rulepacks.up.sql).
type defaultRulepackSpec struct {
	DisplayName string
	Tags        []string
	Rules       []defaultGuardRailRuleSpec
}

type defaultGuardRailRuleSpec struct {
	ShortName   string
	Description string
	InputJSON   string
	OutputJSON  string
}

const emptyRulesJSON = `{"rules": []}`

var defaultRulepackCatalog = []defaultRulepackSpec{
	{
		DisplayName: "PostgreSQL Security Pack",
		Tags:        []string{"postgres", "database"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "pg-block-unsafe-write",
				Description: "PostgreSQL: block UPDATE/DELETE without WHERE",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "pg-block-destructive-ddl",
				Description: "PostgreSQL: block DROP/TRUNCATE on tables, schemas, databases",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["DROP TABLE", "TRUNCATE", "DROP DATABASE", "DROP SCHEMA"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "pg-mask-pgcrypto-output",
				Description: "PostgreSQL: redact filesystem-access function output",
				InputJSON:   emptyRulesJSON,
				OutputJSON:  `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(pg_read_file|pg_ls_dir|pg_stat_file)\\b"}]}`,
			},
		},
	},
	{
		DisplayName: "MySQL Security Pack",
		Tags:        []string{"mysql", "database"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "mysql-block-unsafe-write",
				Description: "MySQL: block UPDATE/DELETE without WHERE",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "mysql-block-grant-changes",
				Description: "MySQL: block privilege and authentication changes",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["GRANT", "REVOKE", "CREATE USER", "DROP USER", "SET PASSWORD"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "mysql-block-load-infile",
				Description: "MySQL: block filesystem read/write via SQL",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bLOAD\\s+DATA\\b|\\bINTO\\s+OUTFILE\\b|\\bINTO\\s+DUMPFILE\\b"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
		},
	},
	{
		DisplayName: "MongoDB Security Pack",
		Tags:        []string{"mongodb", "database"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "mongo-block-drop",
				Description: "MongoDB: block collection and database drops",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["dropDatabase", "db.dropDatabase", ".drop(", "dropAllUsers"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "mongo-block-unfiltered-write",
				Description: "MongoDB: block updateMany/deleteMany with empty filter",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\.(updateMany|deleteMany)\\s*\\(\\s*\\{\\s*\\}"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "mongo-block-eval",
				Description: "MongoDB: block server-side JavaScript execution",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["db.eval", "$where", "mapReduce"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
		},
	},
	{
		DisplayName: "DynamoDB Security Pack",
		Tags:        []string{"dynamodb", "database", "aws"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "ddb-block-table-delete",
				Description: "DynamoDB: block table deletion API calls",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["DeleteTable", "delete-table"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "ddb-block-full-scan",
				Description: "DynamoDB: block Scan without FilterExpression",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bScan\\b(?!.*\\bFilterExpression\\b)"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "ddb-block-batch-purge",
				Description: "DynamoDB: block bulk deletes via BatchWriteItem",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)BatchWriteItem.*DeleteRequest"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
		},
	},
	{
		DisplayName: "Oracle DB Security Pack",
		Tags:        []string{"oracledb", "database"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "oracle-block-unsafe-write",
				Description: "Oracle: block UPDATE/DELETE without WHERE",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "oracle-block-shutdown",
				Description: "Oracle: block instance/system control statements",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["SHUTDOWN", "STARTUP", "ALTER SYSTEM", "ALTER DATABASE"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "oracle-block-utl-packages",
				Description: "Oracle: block filesystem and network UTL/DBMS packages",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(UTL_FILE|UTL_HTTP|UTL_SMTP|DBMS_LOB\\.LOADFROMFILE)\\b"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
		},
	},
	{
		DisplayName: "MSSQL Security Pack",
		Tags:        []string{"mssql", "database"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "mssql-block-unsafe-write",
				Description: "MSSQL: block UPDATE/DELETE without WHERE",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)^\\s*(UPDATE|DELETE)\\s+\\S+(?!.*\\bWHERE\\b).*"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "mssql-block-xp-cmdshell",
				Description: "MSSQL: block xp_cmdshell and OLE automation procs",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["xp_cmdshell", "sp_configure", "EXEC sp_OACreate"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "mssql-block-bulk-io",
				Description: "MSSQL: block BULK INSERT and OPENROWSET I/O",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bBULK\\s+INSERT\\b|\\bOPENROWSET\\b|\\bOPENDATASOURCE\\b"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
		},
	},
	{
		DisplayName: "Claude Code Security Pack",
		Tags:        []string{"claude-code", "ai"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "claude-block-shell-exec",
				Description: "Claude Code: block destructive/piped-shell prompt patterns",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(rm\\s+-rf\\s+/|sudo\\s+|curl\\s+.*\\|\\s*sh|wget\\s+.*\\|\\s*sh)\\b"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "claude-mask-pii-output",
				Description: "Claude Code: redact SSN/email/PAN in model output",
				InputJSON:   emptyRulesJSON,
				OutputJSON:  `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(\\d{3}-\\d{2}-\\d{4}|[\\w.+-]+@[\\w-]+\\.[\\w.-]+|\\d{13,19})\\b"}]}`,
			},
			{
				ShortName:   "claude-block-prompt-injection",
				Description: "Claude Code: block common prompt-injection phrases",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["ignore previous instructions", "disregard the system prompt", "you are now", "developer mode"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
		},
	},
	{
		DisplayName: "AWS CLI Security Pack",
		Tags:        []string{"aws-cli", "aws"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "aws-block-iam-mutations",
				Description: "AWS CLI: block IAM identity/policy mutations",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(iam\\s+(create|delete|attach|put)-(user|policy|role|access-key))\\b"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "aws-block-account-destruction",
				Description: "AWS CLI: block resource teardown commands",
				InputJSON:   `{"rules": [{"type": "deny_words_list", "words": ["delete-bucket", "terminate-instances", "delete-db-instance", "delete-cluster", "delete-stack"], "pattern_regex": ""}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "aws-mask-credentials-output",
				Description: "AWS CLI: redact access keys in output",
				InputJSON:   emptyRulesJSON,
				OutputJSON:  `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\b(AKIA[0-9A-Z]{16}|aws_secret_access_key\\s*=\\s*\\S+)\\b"}]}`,
			},
		},
	},
	{
		DisplayName: "Kubernetes Security Pack",
		Tags:        []string{"kubernetes"},
		Rules: []defaultGuardRailRuleSpec{
			{
				ShortName:   "k8s-block-destructive",
				Description: "Kubernetes: block cluster-wide deletes",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bkubectl\\s+delete\\s+(ns|namespace|all|crd|nodes?)\\b"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "k8s-block-privileged-exec",
				Description: "Kubernetes: block interactive shells into pods",
				InputJSON:   `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\\bkubectl\\s+exec\\b.*--\\s*(sh|bash)\\b"}]}`,
				OutputJSON:  emptyRulesJSON,
			},
			{
				ShortName:   "k8s-mask-secret-output",
				Description: "Kubernetes: redact Secret data fields in output",
				InputJSON:   emptyRulesJSON,
				OutputJSON:  `{"rules": [{"type": "pattern_match", "words": [], "pattern_regex": "(?i)\"(data|stringData)\":\\s*\\{[^}]*\\}"}]}`,
			},
		},
	},
}

// SeedDefaultRulepacksForOrg installs every managed default rulepack (and its
// nested guardrail rules + auto-derived attribute) for the given org. Each
// rulepack is processed independently and idempotently: if the rulepack
// already exists (display_name collision) the entry is skipped without error,
// so it is safe to call this multiple times for the same org.
//
// orgID must be a canonical UUID string. Returns an error only on
// non-recoverable failures (invalid orgID, malformed catalog JSON,
// unexpected DB errors). Per-rulepack errors are logged and do not abort
// the remaining work.
func SeedDefaultRulepacksForOrg(ctx context.Context, orgID string) error {
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		return fmt.Errorf("invalid org id %q: %w", orgID, err)
	}

	logger := log.With("org_id", orgID)
	seeded, skipped := 0, 0
	for _, spec := range defaultRulepackCatalog {
		rp, rules, err := buildDefaultRulepackInput(orgUUID, spec)
		if err != nil {
			return fmt.Errorf("default rulepack %q: %w", spec.DisplayName, err)
		}

		_, _, err = CreateRulepackWithRules(ctx, rp, rules)
		switch {
		case err == nil:
			seeded++
		case errors.Is(err, models.ErrAlreadyExists):
			skipped++
		default:
			logger.With("rulepack", spec.DisplayName).
				Errorf("failed seeding default rulepack: %v", err)
		}
	}
	logger.Infof("default rulepacks seeded=%d skipped=%d total=%d",
		seeded, skipped, len(defaultRulepackCatalog))
	return nil
}

func buildDefaultRulepackInput(orgID uuid.UUID, spec defaultRulepackSpec) (
	*models.Rulepack, RulepackRulesInput, error,
) {
	version := defaultRulepackVersion
	rp := &models.Rulepack{
		OrgID:       orgID,
		DisplayName: spec.DisplayName,
		Version:     &version,
		Tags:        pq.StringArray(spec.Tags),
		IsManaged:   true,
	}

	guardRails := make([]openapi.GuardRailRuleRequest, 0, len(spec.Rules))
	for _, r := range spec.Rules {
		input, err := decodeRuleJSON(r.InputJSON)
		if err != nil {
			return nil, RulepackRulesInput{}, fmt.Errorf("rule %q input: %w", r.ShortName, err)
		}
		output, err := decodeRuleJSON(r.OutputJSON)
		if err != nil {
			return nil, RulepackRulesInput{}, fmt.Errorf("rule %q output: %w", r.ShortName, err)
		}
		guardRails = append(guardRails, openapi.GuardRailRuleRequest{
			Name:        r.ShortName,
			Description: r.Description,
			Input:       input,
			Output:      output,
		})
	}

	return rp, RulepackRulesInput{GuardRailRules: guardRails}, nil
}

func decodeRuleJSON(s string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

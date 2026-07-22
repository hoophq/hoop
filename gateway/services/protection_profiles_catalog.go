// Code generated from docs/protection-profiles-rules.json (schema 2.6.0). DO NOT EDIT BY HAND
// without updating the JSON catalog; the JSON file is the design source of truth
// shared with the frontend.
package services

// protection profile ids (orgs.default_protection_profile). NULL/nil = manual configuration.
const (
	ProtectionProfileHipaaReady           = "hipaa-ready"
	ProtectionProfileSoc2Type2            = "soc2-type2"
	ProtectionProfileProtectionPermissive = "protection-permissive"
	ProtectionProfileProtectionMedium     = "protection-medium"
	ProtectionProfileProtectionHigh       = "protection-high"
)

type protectionGuardrailSpec struct {
	Name        string
	Description string
	InputJSON   string
	OutputJSON  string
}

type protectionMaskingSpec struct {
	Name                 string
	Description          string
	ScoreThreshold       float64
	SupportedEntityTypes string // JSON array, models.SupportedEntityTypesList shape
	CustomEntityTypes    string // JSON array, models.CustomEntityTypesList shape
}

type protectionAccessRuleSpec struct {
	Name              string
	Description       string
	AccessType        string
	AccessMaxDuration int // seconds; 0 = unset (command approval)
	MinApprovals      int
}

type protectionAnalyzerSpec struct {
	Name               string
	Description        string
	RiskEvaluationJSON string // models.AISessionAnalyzerRiskEvaluation shape
}

type protectionProfileSpec struct {
	ID            string
	DisplayName   string
	AttributeName string
	Guardrails    []string
	Masking       []string
	AccessRules   []string
	Analyzers     []string
}

var protectionGuardrailCatalog = map[string]protectionGuardrailSpec{
	"Hoop - Unsafe update and delete": {
		Name:        "Hoop - Unsafe update and delete",
		Description: "Blocks UPDATE or DELETE statements that have no WHERE clause.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?is)^\\\\s*(?:update|delete)\\\\b(?:\\\\s+(?:[^\\\\s;wW][^\\\\s;]*|[wW](?:[^\\\\s;hH][^\\\\s;]*)?|[wW][hH](?:[^\\\\s;eE][^\\\\s;]*)?|[wW][hH][eE](?:[^\\\\s;rR][^\\\\s;]*)?|[wW][hH][eE][rR](?:[^\\\\s;eE][^\\\\s;]*)?|[wW][hH][eE][rR][eE][^\\\\s;]+))*\\\\s*;?\\\\s*$\",\"message\": \"UPDATE and DELETE need a WHERE clause on this connection.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - Destructive DDL": {
		Name:        "Hoop - Destructive DDL",
		Description: "Blocks DROP, TRUNCATE and ALTER TABLE statements.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)\\\\b(drop|truncate)\\\\s+(table|database|schema|index|view)\\\\b|\\\\balter\\\\s+table\\\\b\",\"message\": \"Destructive DDL is blocked. Use a reviewed change process.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - Select without limit": {
		Name:        "Hoop - Select without limit",
		Description: "Blocks SELECT statements that have no LIMIT clause.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?is)^\\\\s*select\\\\b(?:\\\\s+(?:[^\\\\s;lL][^\\\\s;]*|[lL](?:[^\\\\s;iI][^\\\\s;]*)?|[lL][iI](?:[^\\\\s;mM][^\\\\s;]*)?|[lL][iI][mM](?:[^\\\\s;iI][^\\\\s;]*)?|[lL][iI][mM][iI](?:[^\\\\s;tT][^\\\\s;]*)?|[lL][iI][mM][iI][tT][^\\\\s;]+))*\\\\s*;?\\\\s*$\",\"message\": \"Add a LIMIT clause. Unbounded SELECTs are blocked on this connection.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - Select star on sensitive tables": {
		Name:        "Hoop - Select star on sensitive tables",
		Description: "Blocks SELECT * against tables commonly holding personal or health data. Table list is configurable.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)select\\\\s+\\\\*\\\\s+from\\\\s+\\\\S*(users?|patients?|customers?|accounts?|members?|people|employees?)\\\\b\",\"message\": \"SELECT * is blocked on sensitive tables. Select only the columns you need.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - PHI columns": {
		Name:        "Hoop - PHI columns",
		Description: "Blocks queries that reference column names typical of protected health information.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)\\\\b(ssn|social_security(_number)?|date_of_birth|dob|mrn|medical_record(_number)?|diagnosis|icd_?10|icd_?9|npi|health_plan(_id)?|prescription|lab_result)\\\\b\",\"message\": \"This query references protected health information columns and is blocked.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - Bulk export": {
		Name:        "Hoop - Bulk export",
		Description: "Blocks bulk export statements and dump utilities.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)(\\\\bcopy\\\\b[^;]*\\\\bto\\\\b|into\\\\s+(outfile|dumpfile)|\\\\bpg_dump\\\\b|\\\\bmysqldump\\\\b|\\\\bmongodump\\\\b|\\\\bmongoexport\\\\b|\\\\bbcp\\\\b|\\\\bexpdp\\\\b)\",\"message\": \"Bulk export is blocked on this connection.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - Redis flush": {
		Name:        "Hoop - Redis flush",
		Description: "Blocks FLUSHALL, FLUSHDB, SHUTDOWN, CONFIG SET and KEYS * on Redis connections.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)^\\\\s*(flushall|flushdb|shutdown|config\\\\s+set)\\\\b|(?i)\\\\bkeys\\\\s+\\\\*\",\"message\": \"Destructive or blocking Redis commands are not allowed here.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - MongoDB drops": {
		Name:        "Hoop - MongoDB drops",
		Description: "Blocks dropDatabase, collection drop and deleteMany with an empty filter.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)(dropDatabase\\\\s*\\\\(|\\\\.drop\\\\s*\\\\(\\\\s*\\\\)|deleteMany\\\\s*\\\\(\\\\s*\\\\{\\\\s*\\\\}\\\\s*\\\\)|remove\\\\s*\\\\(\\\\s*\\\\{\\\\s*\\\\}\\\\s*\\\\))\",\"message\": \"Destructive MongoDB operations are blocked on this connection.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - Dangerous shell commands": {
		Name:        "Hoop - Dangerous shell commands",
		Description: "Blocks filesystem-destroying commands and reads of credential material in shell sessions.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)(rm\\\\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)[a-z]*\\\\s+/(\\\\s|$)|\\\\bmkfs(\\\\.|\\\\s)|dd\\\\s+if=\\\\S+\\\\s+of=/dev/|>\\\\s*/dev/sd[a-z]|chmod\\\\s+-?R?\\\\s*777\\\\s+/(\\\\s|$))\",\"message\": \"Destructive filesystem commands are blocked.\"},{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)\\\\b(cat|less|more|head|tail|vim?|nano|scp|base64)\\\\b\\\\s+\\\\S*(\\\\.aws/credentials|\\\\.env(\\\\s|$)|id_rsa(\\\\s|$|\\\\.)|\\\\.ssh/|\\\\.netrc|\\\\.pgpass)\",\"message\": \"Reading credential files is blocked in this session.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - Risky kubectl": {
		Name:        "Hoop - Risky kubectl",
		Description: "Blocks kubectl delete, drain, cordon and secret dumps.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)kubectl\\\\s+(delete|drain|cordon)\\\\b\",\"message\": \"Cluster-mutating kubectl commands need an approved workflow.\"},{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)kubectl\\\\s+get\\\\s+secrets?\\\\b.*(-o\\\\s*(yaml|json)|--output[= ](yaml|json))\",\"message\": \"Dumping Kubernetes secrets is blocked.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - Pipe to shell": {
		Name:        "Hoop - Pipe to shell",
		Description: "Blocks curl|sh, wget|sh and ad-hoc package installs on production hosts.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)(curl|wget)[^|;\\\\n]*\\\\|\\\\s*(sudo\\\\s+)?(ba|z|da)?sh\\\\b\",\"message\": \"Piping downloads into a shell is blocked.\"},{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)\\\\b(apt(-get)?|yum|dnf|apk)\\\\s+install\\\\b|\\\\bpip3?\\\\s+install\\\\b|\\\\bnpm\\\\s+install\\\\s+-g\\\\b\",\"message\": \"Ad-hoc package installs are blocked on this host.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - IAM escalation": {
		Name:        "Hoop - IAM escalation",
		Description: "Blocks AWS IAM access key creation, user creation and policy attachment from sessions.",
		InputJSON:   "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"(?i)aws\\\\s+iam\\\\s+(create-access-key|create-user|create-login-profile|attach-(user|role|group)-policy|put-(user|role|group)-policy|update-assume-role-policy)\",\"message\": \"IAM mutations are blocked from interactive sessions.\"}]}",
		OutputJSON:  "{\"rules\": []}",
	},
	"Hoop - SSN in output": {
		Name:        "Hoop - SSN in output",
		Description: "Output rule. Flags US SSN patterns in session output as a second layer behind live data masking.",
		InputJSON:   "{\"rules\": []}",
		OutputJSON:  "{\"rules\": [{\"type\": \"pattern_match\",\"words\": [],\"pattern_regex\": \"\\\\b\\\\d{3}-\\\\d{2}-\\\\d{4}\\\\b\",\"message\": \"Output contains SSN-shaped data and was blocked by policy.\"}]}",
	},
}

var protectionMaskingCatalog = map[string]protectionMaskingSpec{
	"Hoop - PHI strict": {
		Name:                 "Hoop - PHI strict",
		Description:          "Strict masking of PHI and PII. Low threshold to favor over-masking of health data.",
		ScoreThreshold:       0.4,
		SupportedEntityTypes: "[{\"name\": \"PII\",\"entity_types\": [\"PERSON\",\"EMAIL_ADDRESS\",\"PHONE_NUMBER\",\"LOCATION\",\"DATE_TIME\",\"IP_ADDRESS\",\"URL\"]},{\"name\": \"GOVERNMENT_ID\",\"entity_types\": [\"US_SSN\",\"US_PASSPORT\",\"US_DRIVER_LICENSE\",\"US_ITIN\"]},{\"name\": \"FINANCIAL\",\"entity_types\": [\"CREDIT_CARD\",\"US_BANK_NUMBER\",\"IBAN_CODE\",\"CRYPTO\"]},{\"name\": \"HEALTH\",\"entity_types\": [\"MEDICAL_LICENSE\"]}]",
		CustomEntityTypes:    "[{\"name\": \"MEDICAL_RECORD_NUMBER\",\"regex\": \"\\\\bMRN[-:\\\\s]?\\\\d{5,10}\\\\b\",\"score\": 0.7},{\"name\": \"HEALTH_PLAN_ID\",\"regex\": \"\\\\bHPL[-:\\\\s]?\\\\d{4,10}\\\\b\",\"score\": 0.7},{\"name\": \"ICD10_CODE\",\"regex\": \"\\\\b[A-TV-Z][0-9][0-9AB]\\\\.?[0-9A-TV-Z]{0,4}\\\\b\",\"score\": 0.3},{\"name\": \"NPI_NUMBER\",\"regex\": \"\\\\b\\\\d{10}\\\\b\",\"score\": 0.2}]",
	},
	"Hoop - Confidential data": {
		Name:                 "Hoop - Confidential data",
		Description:          "Masks personal, financial and credential data. Balanced threshold for daily production use.",
		ScoreThreshold:       0.6,
		SupportedEntityTypes: "[{\"name\": \"PII\",\"entity_types\": [\"PERSON\",\"EMAIL_ADDRESS\",\"PHONE_NUMBER\",\"LOCATION\",\"IP_ADDRESS\"]},{\"name\": \"GOVERNMENT_ID\",\"entity_types\": [\"US_SSN\",\"US_PASSPORT\",\"US_DRIVER_LICENSE\"]},{\"name\": \"FINANCIAL\",\"entity_types\": [\"CREDIT_CARD\",\"US_BANK_NUMBER\",\"IBAN_CODE\"]}]",
		CustomEntityTypes:    "[{\"name\": \"API_KEY\",\"regex\": \"\\\\b(sk|pk|rk)_(live|test|prod)_[A-Za-z0-9]{16,}\\\\b\",\"score\": 0.8},{\"name\": \"AWS_ACCESS_KEY\",\"regex\": \"\\\\b(AKIA|ASIA)[A-Z0-9]{16}\\\\b\",\"score\": 0.9},{\"name\": \"BEARER_TOKEN\",\"regex\": \"(?i)bearer\\\\s+[A-Za-z0-9\\\\-_\\\\.=]{20,}\",\"score\": 0.7}]",
	},
	"Hoop - Full masking": {
		Name:                 "Hoop - Full masking",
		Description:          "Masks all supported sensitive data types at an aggressive threshold.",
		ScoreThreshold:       0.5,
		SupportedEntityTypes: "[{\"name\": \"PII\",\"entity_types\": [\"PERSON\",\"EMAIL_ADDRESS\",\"PHONE_NUMBER\",\"LOCATION\",\"DATE_TIME\",\"IP_ADDRESS\",\"URL\"]},{\"name\": \"GOVERNMENT_ID\",\"entity_types\": [\"US_SSN\",\"US_PASSPORT\",\"US_DRIVER_LICENSE\",\"US_ITIN\"]},{\"name\": \"FINANCIAL\",\"entity_types\": [\"CREDIT_CARD\",\"US_BANK_NUMBER\",\"IBAN_CODE\",\"CRYPTO\"]}]",
		CustomEntityTypes:    "[{\"name\": \"API_KEY\",\"regex\": \"\\\\b(sk|pk|rk)_(live|test|prod)_[A-Za-z0-9]{16,}\\\\b\",\"score\": 0.8},{\"name\": \"AWS_ACCESS_KEY\",\"regex\": \"\\\\b(AKIA|ASIA)[A-Z0-9]{16}\\\\b\",\"score\": 0.9},{\"name\": \"BEARER_TOKEN\",\"regex\": \"(?i)bearer\\\\s+[A-Za-z0-9\\\\-_\\\\.=]{20,}\",\"score\": 0.7}]",
	},
	"Hoop - API Keys": {
		Name:                 "Hoop - API Keys",
		Description:          "Masks API Keys only. High threshold keeps false positives near zero.",
		ScoreThreshold:       0.85,
		SupportedEntityTypes: "[]",
		CustomEntityTypes:    "[{\"name\": \"API_KEY\",\"regex\": \"\\\\b(sk|pk|rk)_(live|test|prod)_[A-Za-z0-9]{16,}\\\\b\",\"score\": 0.9}]",
	},
	"Hoop - Credentials only": {
		Name:                 "Hoop - Credentials only",
		Description:          "Masks credentials only. High threshold keeps false positives near zero.",
		ScoreThreshold:       0.85,
		SupportedEntityTypes: "[]",
		CustomEntityTypes:    "[{\"name\": \"API_KEY\",\"regex\": \"\\\\b(sk|pk|rk)_(live|test|prod)_[A-Za-z0-9]{16,}\\\\b\",\"score\": 0.9},{\"name\": \"AWS_ACCESS_KEY\",\"regex\": \"\\\\b(AKIA|ASIA)[A-Z0-9]{16}\\\\b\",\"score\": 0.9}]",
	},
}

var protectionAccessRuleCatalog = map[string]protectionAccessRuleSpec{
	"Hoop-JIT_2_hours": {
		Name:              "Hoop-JIT_2_hours",
		Description:       "Just-in-time access. 2-hour maximum, approval required, auto-expires.",
		AccessType:        "jit",
		AccessMaxDuration: 7200,
		MinApprovals:      1,
	},
	"Hoop-JIT_4_hours": {
		Name:              "Hoop-JIT_4_hours",
		Description:       "Just-in-time access. 4-hour maximum, approval required, auto-expires.",
		AccessType:        "jit",
		AccessMaxDuration: 14400,
		MinApprovals:      1,
	},
	"Hoop-JIT_8_hours": {
		Name:              "Hoop-JIT_8_hours",
		Description:       "Just-in-time access. 8-hour maximum, approval required, auto-expires.",
		AccessType:        "jit",
		AccessMaxDuration: 28800,
		MinApprovals:      1,
	},
	"Hoop-Command_approval": {
		Name:              "Hoop-Command_approval",
		Description:       "Command-level approval. Each approved execution is change-management evidence.",
		AccessType:        "command",
		AccessMaxDuration: 0,
		MinApprovals:      1,
	},
}

var protectionAnalyzerCatalog = map[string]protectionAnalyzerSpec{
	"Hoop - Block high risk PHI": {
		Name:               "Hoop - Block high risk PHI",
		Description:        "Blocks high-risk sessions, routes medium-risk to approval. PHI access is treated as high risk.",
		RiskEvaluationJSON: "{\"low_risk\": {\"action\": \"allow_execution\"},\"medium_risk\": {\"action\": \"require_access_request\",\"access_request_rule_name\": \"Hoop-Command_approval\"},\"high_risk\": {\"action\": \"block_execution\"}}",
	},
	"Hoop - Block high risk": {
		Name:               "Hoop - Block high risk",
		Description:        "Blocks high-risk sessions, routes medium-risk sessions to approval.",
		RiskEvaluationJSON: "{\"low_risk\": {\"action\": \"allow_execution\"},\"medium_risk\": {\"action\": \"require_access_request\",\"access_request_rule_name\": \"Hoop-Command_approval\"},\"high_risk\": {\"action\": \"block_execution\"}}",
	},
	"Hoop - Review high risk": {
		Name:               "Hoop - Review high risk",
		Description:        "Routes high-risk sessions to approval.",
		RiskEvaluationJSON: "{\"low_risk\": {\"action\": \"allow_execution\"},\"medium_risk\": {\"action\": \"allow_execution\"},\"high_risk\": {\"action\": \"require_access_request\",\"access_request_rule_name\": \"Hoop-Command_approval\"}}",
	},
}

// protectionProfileCatalog maps each selectable profile to the rules it references.
// Order follows the onboarding page: compliance profiles first, then protection levels.
var protectionProfileCatalog = map[string]protectionProfileSpec{
	"hipaa-ready": {
		ID:            "hipaa-ready",
		DisplayName:   "HIPAA Ready",
		AttributeName: "hoop_protection_profile_hipaa_ready",
		Guardrails:    []string{"Hoop - Unsafe update and delete", "Hoop - Destructive DDL", "Hoop - Select star on sensitive tables", "Hoop - PHI columns", "Hoop - Bulk export", "Hoop - Redis flush", "Hoop - MongoDB drops", "Hoop - Dangerous shell commands", "Hoop - Risky kubectl", "Hoop - Pipe to shell", "Hoop - IAM escalation", "Hoop - SSN in output"},
		Masking:       []string{"Hoop - PHI strict"},
		AccessRules:   []string{"Hoop-JIT_4_hours", "Hoop-Command_approval"},
		Analyzers:     []string{"Hoop - Block high risk PHI"},
	},
	"soc2-type2": {
		ID:            "soc2-type2",
		DisplayName:   "SOC 2 Type II",
		AttributeName: "hoop_protection_profile_soc2_type2",
		Guardrails:    []string{"Hoop - Unsafe update and delete", "Hoop - Destructive DDL", "Hoop - Bulk export", "Hoop - Redis flush", "Hoop - MongoDB drops", "Hoop - Dangerous shell commands", "Hoop - Risky kubectl", "Hoop - Pipe to shell", "Hoop - IAM escalation"},
		Masking:       []string{"Hoop - Confidential data"},
		AccessRules:   []string{"Hoop-JIT_8_hours", "Hoop-Command_approval"},
		Analyzers:     []string{"Hoop - Review high risk"},
	},
	"protection-permissive": {
		ID:            "protection-permissive",
		DisplayName:   "Essential Guardrails",
		AttributeName: "hoop_protection_profile_protection_permissive",
		Guardrails:    []string{"Hoop - Unsafe update and delete"},
		Masking:       []string{"Hoop - API Keys"},
		AccessRules:   []string{},
		Analyzers:     []string{},
	},
	"protection-medium": {
		ID:            "protection-medium",
		DisplayName:   "Balanced",
		AttributeName: "hoop_protection_profile_protection_medium",
		Guardrails:    []string{"Hoop - Unsafe update and delete", "Hoop - Destructive DDL", "Hoop - Select star on sensitive tables", "Hoop - Bulk export", "Hoop - Redis flush", "Hoop - MongoDB drops", "Hoop - Dangerous shell commands"},
		Masking:       []string{"Hoop - Confidential data"},
		AccessRules:   []string{"Hoop-JIT_8_hours", "Hoop-Command_approval"},
		Analyzers:     []string{"Hoop - Review high risk"},
	},
	"protection-high": {
		ID:            "protection-high",
		DisplayName:   "Maximum",
		AttributeName: "hoop_protection_profile_protection_high",
		Guardrails:    []string{"Hoop - Unsafe update and delete", "Hoop - Destructive DDL", "Hoop - Select without limit", "Hoop - Select star on sensitive tables", "Hoop - Bulk export", "Hoop - Redis flush", "Hoop - MongoDB drops", "Hoop - Dangerous shell commands", "Hoop - Risky kubectl", "Hoop - Pipe to shell", "Hoop - IAM escalation", "Hoop - SSN in output"},
		Masking:       []string{"Hoop - Full masking"},
		AccessRules:   []string{"Hoop-JIT_2_hours", "Hoop-Command_approval"},
		Analyzers:     []string{"Hoop - Block high risk"},
	},
}

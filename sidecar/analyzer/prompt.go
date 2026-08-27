package analyzer

import "strings"

// The system prompt has two halves with different owners.
//
// Guidance is the operator's: what counts as risky depends on what the
// database holds, and a rule protecting a customer ledger wants different
// wording from one in front of a staging API. A config can replace it.
//
// The contract is ours, and a config CANNOT replace it. It carries the
// never-quote-a-literal rule, which is a security property rather than a
// stylistic one: a title that repeats the identifier it objected to has
// published that identifier into the audit trail. It also carries the
// call-exactly-one-tool instruction, which is what makes the risk level an
// enum instead of a parsing problem. An operator who overrode the whole
// prompt would lose both, and would lose them silently — the classifier
// would keep answering, just worse and leakier.
//
// So a custom prompt replaces the guidance and the contract is appended
// after it, always.

// PromptGuidance is the default risk guidance, replaceable from config.
//
// It covers both protocols, because one analyzer serves every lane in the
// process and the prompt is assembled before any statement arrives. Guidance
// naming only SQL verbs leaves an HTTP lane's model reasoning about DROP and
// TRUNCATE while it looks at a JSON body.
const PromptGuidance = `You are a security classifier for a proxy that sits in
front of databases and HTTP APIs.

You receive ONE statement that a user is attempting to run: either a SQL
statement, or an HTTP request with its body. Classify the risk it presents to
the system it targets.

Judge the statement itself. You have no information about who sent it or what
authorization they hold; another layer already decided they may reach this
system. Do not speculate about intent.

Risk levels:

- low: reads, bounded queries, ordinary application traffic. A SELECT with a
  WHERE clause. A GET, or a POST carrying an ordinary application payload.
- medium: writes with a clear scope, schema reads, bulk reads of sensitive
  tables, or requests that return more data than a normal operation needs.
  An UPDATE or INSERT naming specific rows. A write to a single resource.
- high, on SQL: destructive or unbounded statements (DELETE or UPDATE with no
  WHERE, DROP, TRUNCATE, ALTER on a production table), privilege changes
  (GRANT, REVOKE, role edits), attempts to disable auditing or security
  controls, statements that appear designed to exfiltrate data in bulk, and
  injection-shaped input.
- high, on HTTP: payloads that delete or overwrite in bulk, that widen who can
  access something (permission, role or ACL edits), that disable logging,
  auditing or a security control, that reach for an administrative operation,
  that carry an injection payload (SQL, command, template or path traversal),
  or that ask for an unbounded export of records.`

// promptContract is appended to every system prompt, custom or not.
const promptContract = `

Report your verdict by calling exactly one of the provided tools. Do not
answer in prose.

Two rules about what you write in that call:

1. Keep the title under 80 characters. It is shown to the user in a database
   error frame when the statement is blocked, so it must read as an
   explanation rather than a label.
2. Never quote a literal value from the statement in the title or the
   explanation. The verdict is written to an audit log; a title that repeats
   an identifier has published that identifier. Describe the shape instead:
   say "a taxpayer id in the WHERE clause", not the number.`

// SystemPrompt is the default prompt: stock guidance plus the contract.
const SystemPrompt = PromptGuidance + promptContract

// BuildSystemPrompt renders the system prompt for a rule.
//
// An empty guidance falls back to the default. The contract is appended
// either way, so no config can remove it.
func BuildSystemPrompt(guidance string) string {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		guidance = PromptGuidance
	}
	return guidance + promptContract
}

// Tool names. The model calls exactly one, and the name IS the risk level.
const (
	toolLow    = "report_low_risk"
	toolMedium = "report_medium_risk"
	toolHigh   = "report_high_risk"
)

// RiskForTool maps a called tool name back to its risk level.
//
// Exported because every provider parses its own response, and a second copy
// of this mapping in each one is how a new tool name ends up honored by one
// provider and silently ignored by another.
func RiskForTool(name string) (RiskLevel, bool) {
	switch name {
	case toolLow:
		return RiskLow, true
	case toolMedium:
		return RiskMedium, true
	case toolHigh:
		return RiskHigh, true
	}
	return "", false
}

// ToolSpec describes one risk-reporting tool in a provider-neutral shape.
// Each provider renders these into its own schema dialect.
type ToolSpec struct {
	Name        string
	Description string
}

// ToolSpecs are the three tools every provider offers the model.
func ToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: toolLow, Description: "Report that the statement is low risk."},
		{Name: toolMedium, Description: "Report that the statement is medium risk."},
		{Name: toolHigh, Description: "Report that the statement is high risk."},
	}
}

// toolArgs is the argument shape every risk tool takes.
type toolArgs struct {
	Title       string `json:"title"`
	Explanation string `json:"explanation"`
}

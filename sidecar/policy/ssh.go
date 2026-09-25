package policy

import (
	"fmt"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// SSH adds no rule type. It removes four.
//
// The types that work on an SSH lane already exist and need no protocol
// knowledge: pattern_match over the statement's text, operation over the
// twelve SSH operations, pii over what the text carries, ai_analysis over a
// command. What the text IS varies by operation — a command line for
// exec_line, a variable name for env_set, a path for every sftp_* — and a
// rule scopes itself with `operations`, the way it does on any other lane.
//
// The four below have nothing on this lane to read, and a rule that loads,
// evaluates and never fires is the silent failure this package refuses
// everywhere else. They are refused at config load rather than skipped at
// evaluation, because the two failures reach different people: a load
// refusal reaches the operator who wrote the rule, and a silent skip reaches
// nobody at all.
//
//   - table: SSH has no relations. An sftp path is not a table, and reading
//     it as one would make `table: /etc` match by accident rather than by
//     design. Paths are pattern_match's job.
//   - http_resource, http_status, http_header: there is no request, no
//     response code and no header.
//   - grpc_status: there is no RPC.
var sshRefusedRuleTypes = []MatchType{
	MatchTable,
	MatchHTTPResource,
	MatchHTTPStatus,
	MatchHTTPHeader,
	MatchGRPCStatus,
}

// ValidateForSSH reports the rules in a set that an SSH lane cannot evaluate,
// one message per rule.
//
// Exported because the refusal belongs to the config layer — the daemon owns
// startup refusals — while the knowledge of which rule type reads which
// statement field belongs here, beside the matchers themselves. Splitting
// them would leave the daemon carrying a list it cannot check against the
// code that makes it true.
func ValidateForSSH(rules []Rule) []string {
	refused := make(map[MatchType]bool, len(sshRefusedRuleTypes))
	for _, t := range sshRefusedRuleTypes {
		refused[t] = true
	}

	var problems []string
	for _, r := range rules {
		if !refused[r.Type] {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"rule %q is a %q rule, and an %s statement has nothing for it to read: %s",
			r.Name, r.Type, inspect.SSH, sshRefusalReason(r.Type)))
	}
	return problems
}

// sshRefusalReason says what is missing, so the message names the fix rather
// than only the refusal.
func sshRefusalReason(t MatchType) string {
	switch t {
	case MatchTable:
		return "SSH has no relations, and an sftp path is not a table; " +
			"match a path with pattern_match scoped to the sftp_* operations"
	case MatchHTTPResource:
		return "there is no request path; match a command or a path with pattern_match"
	case MatchHTTPStatus:
		return "there is no response status"
	case MatchHTTPHeader:
		return "there is no request header"
	case MatchGRPCStatus:
		return "there is no RPC"
	}
	return "it reads a field this protocol does not produce"
}

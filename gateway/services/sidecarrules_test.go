package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// TestValidateSidecarRuleSpec pins the write gate. Every row is a rule an
// admin can type and that a sidecar would refuse at STARTUP -- which means a
// fleet that crash-loops on its next restart, hours after the save looked
// fine.
func TestValidateSidecarRuleSpec(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind SidecarRuleKind
		spec string
		want string // substring of the refusal; empty means accepted
	}{
		// ---- guardrails: the sidecar's own validator answers -------------
		{
			name: "an operation rule, which the gateway has no concept of",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"operation","operations":["drop","truncate"]}]}`,
		},
		{
			name: "a table rule with the read/write split",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"table","tables":["customers"],"access":"write"}]}`,
		},
		{
			name: "a pii rule, which needs a detector a real sidecar links",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"pii","entities":["BR_CPF"]}]}`,
		},
		{
			name: "a rule that defers the verdict to Rego",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"pii","entities":["BR_CPF"],"action":"defer"}]}`,
		},
		{
			// The daemon's own message, not a second opinion about it.
			name: "an access value the daemon does not know",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"table","tables":["t"],"access":"readwrite"}]}`,
			want: "unknown access",
		},
		{
			name: "an action other than defer",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"operation","operations":["drop"],"action":"warn"}]}`,
			want: "unknown action",
		},
		{
			// The sidecar compiles patterns when it LOADS the document, so one
			// bad pattern refuses the whole configuration. Five of the nine
			// seeded gateway rulepacks use negative lookahead, which RE2 rejects.
			name: "a PCRE lookahead RE2 cannot compile",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"pattern_match","pattern_regex":"(?i)^(UPDATE)(?!.*WHERE)"}]}`,
			want: "bad pattern",
		},
		{
			name: "a rule type nothing knows",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"vibes","words":["x"]}]}`,
			want: "unknown rule type",
		},
		{
			name: "a key no sidecar declares",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[{"name":"r","type":"operation","operations":["drop"]}],"enforce":true}`,
			want: "not a shape a sidecar accepts",
		},
		{
			name: "a guardrail block with no rules",
			kind: SidecarRuleGuardrail,
			spec: `{"rules":[]}`,
			want: "enforce nothing",
		},

		// ---- masking: the sidecar's vocabulary, not the gateway's ---------
		{
			name: "an entity rule with a strategy",
			kind: SidecarRuleMask,
			spec: `{"rules":[{"name":"ssn","entities":["US_SSN"],"strategy":"partial","keep_last":4}]}`,
		},
		{
			name: "a column rule, which masks by position rather than detection",
			kind: SidecarRuleMask,
			spec: `{"rules":[{"name":"ssn","columns":["ssn"],"strategy":"hash"}]}`,
		},
		{
			name: "a rule naming neither entities nor columns",
			kind: SidecarRuleMask,
			spec: `{"rules":[{"name":"nothing","strategy":"redact"}]}`,
			want: "neither entities nor columns",
		},
		{
			name: "a strategy the plugin has no implementation for",
			kind: SidecarRuleMask,
			spec: `{"rules":[{"name":"ssn","entities":["US_SSN"],"strategy":"shred"}]}`,
			want: "redact, mask, partial or hash",
		},
		{
			// The gateway's own masking vocabulary, which is a different
			// feature: it would save and mask nothing.
			name: "the gateway's field names",
			kind: SidecarRuleMask,
			spec: `{"rules":[{"name":"x","supported_entity_types":[{"name":"IDENTITY"}]}]}`,
			want: "not a shape a sidecar accepts",
		},

		// ---- analyzer: a block, in the lane's own action vocabulary -------
		{
			name: "a block with a trigger and a blocking tier",
			kind: SidecarRuleAnalyzer,
			spec: `{"trigger":{"operations":["update","delete"]},"high":"block","medium":"warn"}`,
		},
		{
			name: "a block that defers to Rego",
			kind: SidecarRuleAnalyzer,
			spec: `{"high":"defer","prompt":"Treat the ledger as high risk."}`,
		},
		{
			// The GATEWAY's action vocabulary. The two features share a name
			// and nothing else.
			name: "the gateway's risk actions",
			kind: SidecarRuleAnalyzer,
			spec: `{"high":"block_execution"}`,
			want: "allow, warn, block, defer or require_review",
		},
		{
			// The sidecar's own refusal: a block naming no action allows every
			// verdict while still paying for the classification.
			name: "a block naming no action at all",
			kind: SidecarRuleAnalyzer,
			spec: `{"trigger":{"operations":["update"]}}`,
			want: "names no action",
		},
		// ---- the review pairing, which the sidecar refuses at STARTUP -----
		// A hold and the rule that releases it must arrive together. Either
		// alone is a control nobody reads: reviewers no statement ever
		// reaches, or a statement nobody can release.
		{
			name: "an approval rule with no level that holds",
			kind: SidecarRuleAnalyzer,
			spec: `{"high":"block","approval_rule":"payments-review"}`,
			want: "no risk level asks for",
		},
		{
			name: "a hold that names no approval rule",
			kind: SidecarRuleAnalyzer,
			spec: `{"high":"require_review"}`,
			want: "names no approval_rule",
		},
		{
			name: "a hold with its approval rule",
			kind: SidecarRuleAnalyzer,
			spec: `{"high":"require_review","medium":"warn","approval_rule":"payments-review"}`,
		},
		{
			name: "an empty spec on a bound rule",
			kind: SidecarRuleAnalyzer,
			spec: ``,
			want: "carries no sidecar configuration",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSidecarRuleSpec(tt.kind, "my-rule", json.RawMessage(tt.spec))
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want the rule accepted, got %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("want a refusal naming %q, got nil", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Errorf("refusal does not name %q: %v", tt.want, err)
			}
			// Whatever the verdict, the message names the rule: an admin
			// reading it has several.
			if err != nil && !strings.Contains(err.Error(), "my-rule") {
				t.Errorf("refusal does not name the rule: %v", err)
			}
		})
	}
}

// TestValidateSpecForLane pins the refusals that depend on WHICH listener a
// rule lands on. This is the half a rule editor cannot do on its own, and
// every one of them is a startup refusal on the sidecar: a rule saved without
// this check bricks the fleet at its next restart, not at the save.
func TestValidateSpecForLane(t *testing.T) {
	ssh := daemon.ListenerConfig{Name: "bastion", Protocol: "ssh"}
	pg := daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"}
	analyzed := daemon.ListenerConfig{Name: "appdb", Protocol: "postgres",
		Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 40}}
	analyzedHTTP := daemon.ListenerConfig{Name: "api", Protocol: "http",
		Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 40}}
	analyzedGRPC := daemon.ListenerConfig{Name: "ledger", Protocol: "grpc",
		Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 40}}
	analyzedShell := daemon.ListenerConfig{Name: "bastion", Protocol: "ssh",
		Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 40}}
	noShell := daemon.Capabilities{"exec", "sftp"}
	analyzedExec := daemon.ListenerConfig{Name: "bastion", Protocol: "ssh",
		SSH:      &daemon.SSHConfig{CapabilitiesAllowed: &noShell},
		Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 40}}

	for _, tt := range []struct {
		name string
		kind SidecarRuleKind
		lane daemon.ListenerConfig
		spec string
		want string
		// hasAnalyzer is the sidecar's top-level analyzer section.
		hasAnalyzer bool
	}{
		{
			// SSH has no relations, so the rule would load, evaluate and match
			// nothing -- which is why the daemon refuses it at load instead.
			name: "a table rule on an ssh lane",
			kind: SidecarRuleGuardrail,
			lane: ssh,
			spec: `{"rules":[{"name":"r","type":"table","tables":["customers"]}]}`,
			want: "bastion",
		},
		{
			name: "a pattern rule scoped to sftp operations, which ssh does read",
			kind: SidecarRuleGuardrail,
			lane: ssh,
			spec: `{"rules":[{"name":"r","type":"pattern_match","pattern_regex":"secrets",` +
				`"operations":["sftp_read","exec_line"]}]}`,
		},
		{
			name: "the same table rule on a postgres lane",
			kind: SidecarRuleGuardrail,
			lane: pg,
			spec: `{"rules":[{"name":"r","type":"table","tables":["customers"]}]}`,
		},
		{
			// An ssh lane rewrites a byte stream in place: a replacement of a
			// different size shifts every byte after it and desynchronizes the
			// terminal. Length preservation is the safety property.
			name: "redact on an ssh lane",
			kind: SidecarRuleMask,
			lane: ssh,
			spec: `{"rules":[{"name":"e","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]}`,
			want: "masks bytes in place",
		},
		{
			name: "mask on an ssh lane",
			kind: SidecarRuleMask,
			lane: ssh,
			spec: `{"rules":[{"name":"e","entities":["EMAIL_ADDRESS"],"strategy":"mask"}]}`,
		},
		{
			name: "redact on a postgres lane, which rebuilds its row frames",
			kind: SidecarRuleMask,
			lane: pg,
			spec: `{"rules":[{"name":"e","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]}`,
		},
		{
			// The sidecar refuses a lane analyzer block with no provider.
			name: "an analyzer rule on a lane without a block, on a sidecar without the analyzer section",
			kind: SidecarRuleAnalyzer,
			lane: pg,
			spec: `{"high":"block"}`,
			want: "has no analyzer section",
		},
		{
			// The rule's block becomes the lane's.
			name:        "an analyzer rule on a lane without a block, on a sidecar with the analyzer section",
			kind:        SidecarRuleAnalyzer,
			lane:        pg,
			spec:        `{"high":"block"}`,
			hasAnalyzer: true,
		},
		{
			name: "an analyzer rule on a lane that opted in",
			kind: SidecarRuleAnalyzer,
			lane: analyzed,
			spec: `{"high":"block"}`,
		},
		{
			// A hold waits on the connection, and an http caller waits
			// while its own deadline lasts.
			name: "a hold on an http lane",
			kind: SidecarRuleAnalyzer,
			lane: analyzedHTTP,
			spec: `{"high":"require_review","approval_rule":"payments-review"}`,
		},
		{
			name: "a hold on a grpc lane",
			kind: SidecarRuleAnalyzer,
			lane: analyzedGRPC,
			spec: `{"high":"require_review","approval_rule":"payments-review"}`,
		},
		{
			// A shell sends no statements, so what is typed in it walks
			// around the hold. An omitted capability list admits it.
			name: "a hold on an ssh lane that admits shell",
			kind: SidecarRuleAnalyzer,
			lane: analyzedShell,
			spec: `{"high":"require_review","approval_rule":"payments-review"}`,
			want: "drop shell from ssh.capabilities_allowed",
		},
		{
			name: "a hold on an ssh lane without shell",
			kind: SidecarRuleAnalyzer,
			lane: analyzedExec,
			spec: `{"high":"require_review","approval_rule":"payments-review"}`,
		},
		{
			name: "the same hold on a postgres lane",
			kind: SidecarRuleAnalyzer,
			lane: analyzed,
			spec: `{"high":"require_review","approval_rule":"payments-review"}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSpecForLane(tt.kind, "my-rule", json.RawMessage(tt.spec), "payments", tt.lane, tt.hasAnalyzer)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want the binding accepted, got %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("want a refusal naming %q, got nil", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Errorf("refusal does not name %q: %v", tt.want, err)
			}
		})
	}
}

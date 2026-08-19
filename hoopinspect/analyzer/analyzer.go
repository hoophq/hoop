// Package analyzer classifies statements with a language model and turns the
// classification into an allow, warn or block.
//
// It is the third evaluator in the policy chain, after the local rules and
// OPA, and it is the only one that costs money and hundreds of milliseconds
// per call. Everything in this package exists to make that cost visible and
// bounded: a trigger narrows what is worth asking about, a cache collapses
// repeated statement shapes onto one verdict, and a per-session budget stops
// a runaway client from becoming a runaway invoice.
//
// The package holds no provider. A Provider is registered by an
// implementation package (analyzer/anthropic, analyzer/openai,
// analyzer/vertex) and resolved by name, which keeps a provider that needs a
// dependency out of the dependency-free root.
package analyzer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/policy"
)

// RiskLevel is the model's classification of one statement.
//
// Three levels, not a score. The classification comes from which tool the
// model chose to call, so there is no confidence number underneath to expose,
// and inventing one would imply a precision the method does not have.
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// Valid reports whether r is one of the three levels.
func (r RiskLevel) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh:
		return true
	}
	return false
}

// Metadata keys the analyzer writes onto a verdict's annotations, which the
// gate copies onto the audit event.
//
// The vocabulary is fixed and short on purpose. audit.SinkOptions redaction
// fingerprints Statement and HTTP.Body but never touches Event.Metadata, so
// anything written here lands in the trail verbatim regardless of the
// operator's redact_statements setting. Only classifications go here, never
// model prose and never a value the statement carried.
const (
	// MetadataRiskLevel is read by store.MemoryStore and the SQLite store
	// to roll a session's highest risk up onto its record. The key name is
	// theirs, not ours: changing it silently disables the rollup.
	//
	// Defined in policy because policy.Chain has to merge these keys and
	// cannot import this package. One definition, two readers.
	MetadataRiskLevel = policy.AnnotationRiskLevel

	// MetadataAction records what the risk mapped to, so a reviewer can
	// tell a high-risk statement that was allowed under a warn policy from
	// one that was blocked. It merges as a PAIR with MetadataRiskLevel; see
	// policy.mergeAnnotations.
	MetadataAction = policy.AnnotationRiskAction

	// MetadataAIRule names the rule that produced the level, so a lane
	// carrying two ai_analysis rules can be told apart in the trail.
	MetadataAIRule = "ai_rule"

	// MetadataAIStatus records what the analyzer did. It is the key that
	// separates "rated low" from "never asked", "provider down" and
	// "budget spent". All four look like an absent risk_level to anything
	// reading the trail.
	//
	// Defined here rather than in policy because nothing outside this
	// package needs to understand it: the analyzer folds its own findings,
	// so policy.Chain never has to rank a status it cannot interpret.
	MetadataAIStatus = "ai_status"
)

// Keys inside this producer's Finding.Values, as
// `input.findings.ai_analysis.values.*` in a policy's input document.
//
// They are a PUBLIC contract in the same way the input document's field names
// are: renaming one breaks somebody's Rego, and breaks the review gate, which
// reads the action from here rather than keeping a second copy of the
// operator's risk-to-action mapping.
const (
	// FindingRiskLevel is the model's verdict: low, medium or high. Present
	// only on an answered finding.
	FindingRiskLevel = "risk_level"

	// FindingAction is what the rule's configuration mapped that level to.
	//
	// It travels as a PAIR with FindingRiskLevel, for the same reason the
	// annotations do: an action beside no level, or beside a different
	// rule's level, describes a mapping nothing performed.
	FindingAction = "action"
)

// Source is the key this producer's findings appear under in a policy's
// input document, as `input.findings.ai_analysis`.
//
// It matches the rule type an operator writes in YAML, so the thing they
// configured and the thing their Rego addresses have one name.
const Source = string(policy.MatchAIAnalysis)

// Statuses this producer reports. They refine policy's generic set rather
// than replacing it: Budget and Refused are both policy.FindingUnavailable
// with a reason, and the trail keeps the specific word because an operator
// tuning max_calls and an operator tuning send: refuse are chasing different
// things.
const (
	StatusOK      = policy.FindingOK
	StatusCached  = policy.FindingCached
	StatusSkipped = policy.FindingSkipped
	StatusError   = policy.FindingError

	// StatusBudget means the process-wide call budget was spent.
	StatusBudget = "budget_exhausted"

	// StatusRefused means the content carried a detected entity and
	// send=refuse forbade transmitting it.
	StatusRefused = "refused"
)

// findingStatus maps a reported status onto policy's generic vocabulary, so a
// Rego author can write `status == "unavailable"` without learning this
// package's reasons, and read `reason` when they care which.
func findingStatus(status string) string {
	switch status {
	case StatusBudget, StatusRefused:
		return policy.FindingUnavailable
	}
	return status
}

// statusReason names a generic status's specific cause, or nothing when the
// status already says everything.
func statusReason(status string) string {
	switch status {
	case StatusBudget, StatusRefused:
		return status
	}
	return ""
}

// rank orders risk for "keep the highest seen" rollups. An unknown level
// ranks zero so it never displaces a real one.
func (r RiskLevel) rank() int {
	switch r {
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	}
	return 0
}

// Action is what the operator wants done at a given risk level.
type Action string

const (
	// ActionAllow forwards the statement. The verdict is still audited.
	ActionAllow Action = "allow"

	// ActionWarn forwards the statement and records the risk. It is the
	// observe-only setting for one tier of one rule, which is how an
	// operator rolls out a block on high while watching medium.
	ActionWarn Action = "warn"

	// ActionBlock refuses the statement.
	ActionBlock Action = "block"

	// ActionDefer forwards the statement and hands the decision to a later
	// evaluator, which in practice means a decide-phase OPA reading
	// input.findings.ai_analysis.values.risk_level.
	//
	// It moves "high risk means block" out of a line of YAML and into the
	// Rego their InfoSec team already owns. The analyzer still classifies,
	// still annotates and still audits; it stops being the thing that
	// decides.
	//
	// A deferred level with nothing behind it to decide would allow
	// everything, so the sidecar refuses the combination at startup rather
	// than shipping a guardrail that quietly does nothing.
	ActionDefer Action = "defer"

	// ActionRequireReview gates the statement on a human approval held by
	// the hoop gateway.
	//
	// Nothing is HELD: the relay dials the upstream on accept and a
	// synchronous gate cannot park a connection for minutes, so a statement
	// with no approval is refused and the database session ends. The agent
	// polls the gateway and reconnects. See hoopinspect/review.
	//
	// Like ActionDefer, this action does not decide anything here. The
	// analyzer classifies, records the action on its Finding and FORWARDS;
	// the gate that acts on it is a separate evaluator placed after this one
	// in the chain. A level mapped to require_review with no such evaluator
	// behind it therefore allows, which is why sidecar/config.go refuses
	// that combination at startup — the same refusal defer gets when a lane
	// has no OPA to defer to.
	ActionRequireReview Action = "require_review"
)

// actionRank orders actions by how much they restrict, for a fold that has to
// keep the strictest of two.
//
// Block is absent because it never reaches a fold: a blocking verdict denies
// and policy.Chain stops there. An unrecognized action ranks zero so it never
// displaces a real one.
func (a Action) actionRank() int {
	switch a {
	case ActionRequireReview:
		return 3
	case ActionDefer:
		return 2
	case ActionWarn:
		return 1
	}
	return 0
}

// Valid reports whether a is a known action.
func (a Action) Valid() bool {
	switch a {
	case ActionAllow, ActionWarn, ActionBlock, ActionDefer, ActionRequireReview:
		return true
	}
	return false
}

// Result is one classification, before any action is applied.
type Result struct {
	// RiskLevel is the model's verdict.
	RiskLevel RiskLevel

	// Title is a short operator-readable summary. It reaches the end user
	// in the protocol's error frame when the action is block, so it must
	// never quote a value the statement contained.
	Title string

	// Explanation is the model's reasoning. Longer than Title and intended
	// for the audit trail, not the wire.
	Explanation string
}

// Provider performs one classification.
//
// Implementations are registered with Register and selected by name from the
// config. A Provider MUST be safe for concurrent use: one instance serves
// every lane and every connection in the process.
type Provider interface {
	// Name reports the config name this provider answers to.
	Name() string

	// Classify sends content to the model under systemPrompt and returns
	// its verdict.
	//
	// systemPrompt is already assembled by BuildSystemPrompt, so a provider
	// passes it through verbatim and never appends to it. The prompt is a
	// per-CALL argument rather than provider state because two rules on one
	// lane can carry different guidance while sharing one provider, one
	// credential and one token source.
	//
	// It MUST respect ctx: the caller sets a deadline, and a provider that
	// ignores it stalls a proxied connection for as long as the upstream
	// cares to take.
	Classify(ctx context.Context, systemPrompt, content string) (*Result, error)
}

// Content is what gets sent to the model for one statement.
//
// It is built by a Builder rather than assembled inline so that adding a
// codec does not mean editing this package. The registry's promise is that a
// new protocol costs one package and nothing else; a switch on protocol here
// would quietly break it.
type Content struct {
	// Text is the prompt body.
	Text string

	// CacheKey identifies the statement SHAPE, not its bytes. Two
	// statements differing only in a literal MUST produce the same key:
	// the risk is in the shape, and re-classifying `WHERE id = 2` after
	// `WHERE id = 1` buys nothing and costs a round trip.
	CacheKey string
}

// Builder renders a statement into model input for one protocol.
type Builder interface {
	// Protocol reports which protocol this builder renders.
	Protocol() hoopinspect.Protocol

	// Build returns the content for a statement, or ok=false when the
	// statement carries nothing worth classifying (an HTTP request with no
	// body, a codec message that is not a statement at all).
	Build(stmt hoopinspect.Statement, maxBytes int) (Content, bool)
}

// ErrUnknownProvider is returned by NewProvider for an unregistered name.
var ErrUnknownProvider = errors.New("hoopinspect/analyzer: unknown provider")

// Truncate cuts s to at most maxBytes on a rune boundary, appending a marker
// when it cut. A provider's input is bounded so one oversized body cannot
// dominate a bill, and the marker tells the model its input is partial rather
// than letting it reason about a sentence that stops mid-word.
//
// Exported because every Builder needs it and each reimplementing it would
// drift on the boundary rule.
func Truncate(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	const marker = "\n...[truncated]"
	cut := maxBytes
	if cut > len(s) {
		cut = len(s)
	}
	// Back off to a rune boundary so a multi-byte character is not split
	// into replacement bytes.
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + marker
}

// utf8Start reports whether b can begin a UTF-8 rune (i.e. is not a
// continuation byte).
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// normalizeSpace collapses runs of whitespace to a single space and trims the
// ends. Two statements differing only in formatting are the same shape, so
// they must hash to the same cache key.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// describeProviders lists registered provider names for an error message.
func describeProviders(names []string) string {
	if len(names) == 0 {
		return "none are linked into this binary"
	}
	return fmt.Sprintf("linked: %s", strings.Join(names, ", "))
}

// RefuseSentinel is what a Redact function returns for content that must not
// be transmitted at all.
//
// It is a sentinel rather than an error because Redact sits on the data path
// and returns a string; the Evaluator recognizes it and denies locally
// without a network call. The value is unprintable so no real statement can
// collide with it.
const RefuseSentinel = "\x00hoopinspect:refuse\x00"

// fingerprint returns a short stable hash of s, used to fold the system
// prompt into a cache key so a reworded prompt does not keep serving verdicts
// the previous one produced.
func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

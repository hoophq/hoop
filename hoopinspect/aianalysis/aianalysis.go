// Package aianalysis scores inspected statements for risk, so a UI can answer
// "why is this session interesting" without a human reading every statement.
//
// # Advisory, never enforcement
//
// Nothing here denies anything. The policy package is the enforcement path and
// it fails CLOSED on error, because an unevaluated statement is exactly the one
// an attacker wants. Risk analysis is the opposite: it is a ranking signal
// rendered next to a session in a list view, and a ranking signal that takes
// the database offline when a model endpoint times out is a worse outcome than
// an unranked session. So every path here fails OPEN — an Analyzer error is
// skipped, not escalated, and AnalyzeSession keeps going.
//
// Keep it that way. If a deployment wants "high risk blocks the query", that
// belongs in policy, where the failure mode is already deny.
//
// # Vocabulary
//
// The risk levels are low/medium/high and a Verdict carries an outcome, a
// risk level, a title, an explanation and the rule that produced it. That is
// hoop's own AI-review vocabulary, deliberately: two systems that score the
// same session must not disagree about what "high" means or force a UI to
// translate between two enums.
//
// # No model required
//
// HeuristicAnalyzer implements Analyzer with nothing but the standard library,
// so the feature works on a deployment that has no LLM configured. An
// LLM-backed Analyzer is a drop-in replacement, or a Chain of both.
package aianalysis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	"github.com/hoophq/hoopinspect/session"
)

// RiskLevel ranks how much attention a statement deserves. The three values
// match hoop's AI review so a UI renders one badge vocabulary.
type RiskLevel string

const (
	// RiskLow is a recognized statement with nothing notable about it.
	RiskLow RiskLevel = "low"

	// RiskMedium is worth a look: broad reads, schema changes, server errors.
	RiskMedium RiskLevel = "medium"

	// RiskHigh is worth an interruption: unbounded destructive statements,
	// privilege changes, sensitive data leaving.
	RiskHigh RiskLevel = "high"
)

// RiskUnknown is the zero RiskLevel: no analyzer ran, or it had no opinion.
// It is deliberately distinct from RiskLow — "we did not look" and "we looked
// and it was fine" must not render as the same badge.
const RiskUnknown RiskLevel = ""

func (r RiskLevel) String() string { return string(r) }

// Rank orders risk levels so "highest seen" is computable. RiskUnknown ranks
// below RiskLow, and any unrecognized value ranks with it: an analyzer that
// invents a level must not outrank a real one.
func (r RiskLevel) Rank() int {
	switch r {
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

// Valid reports whether r is one of the three defined levels.
func (r RiskLevel) Valid() bool { return r.Rank() > 0 }

// ParseRiskLevel converts a stored string back into a RiskLevel. Store
// backends read the level out of audit.Event.Metadata, where it is a plain
// string, and need to compare levels without reimplementing the ordering.
func ParseRiskLevel(s string) (RiskLevel, bool) {
	r := RiskLevel(strings.ToLower(strings.TrimSpace(s)))
	return r, r.Valid()
}

// MaxRisk returns the higher of two levels.
func MaxRisk(a, b RiskLevel) RiskLevel {
	if b.Rank() > a.Rank() {
		return b
	}
	return a
}

// Verdict is the analysis of one statement.
type Verdict struct {
	// RiskLevel is the headline: what badge the UI draws.
	RiskLevel RiskLevel `json:"risk_level"`

	// Title is a short label for a list row ("Unbounded DELETE").
	Title string `json:"title"`

	// Explanation is the sentence a human reads. It must name the specific
	// trigger — "DELETE with no WHERE clause affects every row of orders" —
	// because a generic "high risk detected" tells the reader nothing they
	// can act on and trains them to ignore the badge.
	Explanation string `json:"explanation"`

	// Rule identifies which analyzer rule fired, for correlation and for
	// suppressing a rule that is noisy in one deployment.
	Rule string `json:"rule"`

	// Score ranks findings WITHIN a risk level, 0..1. Two high-risk findings
	// are not equally urgent: an unbounded DELETE outranks a SELECT on a
	// sensitive table, and a session list sorted only by level cannot say so.
	Score float64 `json:"score"`
}

// Analyzer scores one statement.
//
// A nil Verdict with a nil error means "no opinion" — the analyzer did not
// recognize the statement. That is not the same as low risk, and callers must
// not record it as one.
//
// An error means the analysis failed. Callers MUST treat that as missing
// signal, never as a denial: see the package doc.
type Analyzer interface {
	Analyze(ctx context.Context, stmt hoopinspect.Statement) (*Verdict, error)
}

// SessionVerdict is the rolled-up answer for a whole session — the single
// badge and sentence a session list renders.
type SessionVerdict struct {
	// RiskLevel is the HIGHEST level among Findings, not an average.
	//
	// An average lets one dangerous statement hide behind fifty harmless
	// ones: a session that runs `SELECT 1` fifty times and then
	// `DROP TABLE customers` averages out to low and drops off the screen,
	// which is precisely the session someone needed to see. Risk does not
	// cancel out.
	RiskLevel RiskLevel `json:"risk_level"`

	// Title and Explanation come from the highest-scoring finding at
	// RiskLevel, so the summary names a real statement rather than
	// paraphrasing the set.
	Title       string `json:"title,omitempty"`
	Explanation string `json:"explanation,omitempty"`

	// Findings holds every non-nil verdict in statement order. Statements the
	// analyzer had no opinion on, and statements whose analysis failed, are
	// absent — so len(Findings) is not the statement count.
	Findings []Verdict `json:"findings,omitempty"`
}

// AnalyzeSession runs an analyzer over a session's statements and rolls the
// findings into one session-level answer taking the highest risk.
//
// Fail-open by construction: a statement whose analysis returns an error is
// SKIPPED and the walk continues. There is no error return, because there is
// no caller action that an analysis failure should trigger — the worst case is
// an under-scored session, and refusing to score the other forty-nine
// statements makes that strictly worse.
//
// A cancelled context stops the walk and returns what was scored so far, so a
// UI that navigates away does not leave an analyzer running.
//
// A nil analyzer or no statements yields the zero SessionVerdict, whose
// RiskLevel is RiskUnknown.
func AnalyzeSession(ctx context.Context, a Analyzer, stmts []hoopinspect.Statement) SessionVerdict {
	var sv SessionVerdict
	if a == nil {
		return sv
	}

	var top *Verdict
	var topCount int

	for i := range stmts {
		if ctx.Err() != nil {
			break
		}
		v, err := a.Analyze(ctx, stmts[i])
		if err != nil || v == nil {
			continue
		}
		sv.Findings = append(sv.Findings, *v)

		last := &sv.Findings[len(sv.Findings)-1]
		switch {
		case top == nil || last.RiskLevel.Rank() > top.RiskLevel.Rank():
			top, topCount = last, 1
		case last.RiskLevel.Rank() == top.RiskLevel.Rank():
			topCount++
			// Strictly greater keeps the FIRST of equally-scored findings, so
			// the summary is stable across runs over the same statements.
			if last.Score > top.Score {
				top = last
			}
		}
	}

	if top == nil {
		return sv
	}
	sv.RiskLevel = top.RiskLevel
	sv.Title = top.Title
	sv.Explanation = top.Explanation
	if topCount > 1 {
		sv.Explanation += fmt.Sprintf(" Plus %s at the same level in this session.", plural(topCount-1, "other statement", "other statements"))
	}
	return sv
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Metadata keys a Recorder stamps on the audit event. Exported because a store
// backend indexes on them and must not depend on a copied string literal.
const (
	MetaRiskLevel = "risk_level"
	MetaTitle     = "ai_title"
	MetaRule      = "ai_rule"
)

// Recorder writes verdicts into the audit trail.
//
// # Why metadata and not a new audit.Kind
//
// A KindAIVerdict would force every existing sink, every stored query and
// every UI filter to learn about a kind they do not care about: a JSONL
// consumer grepping for `"kind":"statement"` would silently lose these rows,
// and a store backend that switches on Kind would drop them on the floor. The
// verdict rides on a KindStatement event with three metadata keys instead, so
// a backend that knows nothing about risk still records it, and one that does
// indexes Metadata["risk_level"] without a schema change.
type Recorder struct {
	sink audit.Sink
}

// NewRecorder returns a Recorder writing to sink.
func NewRecorder(sink audit.Sink) *Recorder { return &Recorder{sink: sink} }

// Record emits the verdict as an audit event.
//
// The event is Allowed=true with an empty Event.Rule. Analysis never denies,
// and populating Rule — the field that names the POLICY rule that refused a
// statement — with an analyzer rule would corrupt every "denials by rule"
// aggregate. The analyzer rule goes in Metadata[MetaRule].
//
// Record does NOT attach the statement, because a Verdict does not carry one:
// the KindStatement event the gate already wrote for that statement holds the
// text, and duplicating it here would double the storage for every analyzed
// statement.
func (r *Recorder) Record(ctx context.Context, s *session.Session, v Verdict) error {
	if r == nil || r.sink == nil {
		return errors.New("aianalysis: recorder has no sink")
	}
	if s == nil {
		return errors.New("aianalysis: record needs a session")
	}

	// Metadata is copied rather than aliased: the session's map is shared by
	// every event of the session, and stamping one statement's verdict into
	// it would smear that verdict across the whole trail.
	meta := make(map[string]string, len(s.Metadata)+3)
	for k, val := range s.Metadata {
		meta[k] = val
	}
	meta[MetaRiskLevel] = string(v.RiskLevel)
	meta[MetaTitle] = v.Title
	meta[MetaRule] = v.Rule

	return r.sink.Write(ctx, audit.Event{
		Kind:       audit.KindStatement,
		Timestamp:  time.Now().UTC(),
		SessionID:  s.ID,
		Principal:  s.Identity.Principal(),
		Protocol:   s.Protocol,
		Connection: s.Connection,
		Allowed:    true,
		Message:    v.Explanation,
		Metadata:   meta,
	})
}

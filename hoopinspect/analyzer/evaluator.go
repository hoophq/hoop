package analyzer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/policy"
)

// DefaultTimeout bounds one classification. It is deliberately short: the
// call sits inline on a proxied connection, and a model that has not answered
// in ten seconds has already cost more than its verdict is worth.
const DefaultTimeout = 10 * time.Second

// DefaultMaxInputBytes bounds what is sent per statement.
const DefaultMaxInputBytes = 8 << 10

// Trigger decides whether a statement is worth classifying.
//
// It is the first and cheapest cost control. A lane with no trigger
// classifies everything, which on a database lane means paying for every
// SELECT an ORM emits. An empty Trigger matches nothing, so a rule must state
// what it cares about; that is the safe default, because the failure mode of
// "matches everything by accident" is a bill.
type Trigger struct {
	// Operations matches the statement's normalized verb.
	Operations []hoopinspect.Operation

	// Tables matches any referenced table, lowercased.
	//
	// Tables is best effort in the codec, so a statement whose tables could
	// not be determined does NOT match a table trigger. Trigger on
	// operations for anything load-bearing.
	Tables []string

	// Resources matches an HTTP resource with a glob, using the same
	// matcher as an http_resource policy rule.
	Resources []string
}

// IsZero reports whether the trigger names nothing.
func (t Trigger) IsZero() bool {
	return len(t.Operations) == 0 && len(t.Tables) == 0 && len(t.Resources) == 0
}

// Matches reports whether stmt should be classified.
//
// Exported because the review gate has to answer the same question one
// evaluator earlier in the chain: it claims an existing approval BEFORE the
// analyzer runs, and doing that on every statement would be a gateway
// round-trip per query. It narrows to the same set with the same triggers, and
// a second copy of this matcher is how the two would drift apart.
func (t Trigger) Matches(stmt hoopinspect.Statement) bool {
	for _, op := range t.Operations {
		if stmt.Operation == op {
			return true
		}
	}
	for _, want := range t.Tables {
		for _, got := range stmt.Tables {
			if strings.EqualFold(want, got) {
				return true
			}
		}
	}
	if stmt.HTTP != nil {
		target := stmt.HTTP.Resource
		if target == "" {
			target = stmt.HTTP.Path
		}
		for _, pattern := range t.Resources {
			if policy.MatchResource(pattern, target) {
				return true
			}
		}
	}
	return false
}

// ActionMap maps each risk level to what the operator wants done.
//
// A level with no entry defaults to allow. That is the conservative default
// for a control that costs money and can be wrong: an operator opts into
// blocking a tier by naming it.
type ActionMap map[RiskLevel]Action

func (m ActionMap) actionFor(level RiskLevel) Action {
	if a, ok := m[level]; ok && a != "" {
		return a
	}
	return ActionAllow
}

// Config assembles an Evaluator.
type Config struct {
	// Rule names this analyzer in verdicts and audit records.
	Rule string

	// Provider performs the classification. Required.
	Provider Provider

	// Actions maps risk to enforcement.
	Actions ActionMap

	// Trigger narrows what is classified. A zero Trigger classifies
	// nothing, which makes a misconfigured rule silent rather than
	// expensive.
	Trigger Trigger

	// Message overrides the model's Title in the denial the user reads.
	// Empty uses the Title, which is the more useful default: it tells the
	// developer what the classifier objected to.
	Message string

	// Guidance replaces the default risk guidance in the system prompt.
	// Empty uses PromptGuidance.
	//
	// It replaces the GUIDANCE only. BuildSystemPrompt appends the output
	// contract regardless, so no configuration can drop the
	// never-quote-a-literal rule or the call-one-tool instruction.
	Guidance string

	// Timeout bounds one classification. Zero uses DefaultTimeout.
	Timeout time.Duration

	// FailOpen allows a statement whose classification failed.
	//
	// Default false matches the policy package's convention, but the
	// sidecar config defaults it to TRUE, and deliberately: a classifier
	// that takes the database down when a provider has an outage is worse
	// than no classifier. The divergence is stated here so a library caller
	// choosing false is choosing it knowingly.
	FailOpen bool

	// MaxInputBytes bounds the content sent per statement. Zero uses
	// DefaultMaxInputBytes.
	MaxInputBytes int

	// CacheSize and CacheTTL configure the verdict cache. Either zero
	// disables it.
	CacheSize int
	CacheTTL  time.Duration

	// MaxCallsPerSession bounds classifications for one Evaluator's
	// lifetime. Zero means unbounded.
	//
	// An Evaluator is shared across connections, so this is a process-wide
	// budget rather than a per-connection one. It is a backstop against a
	// pathological workload, not a quota.
	MaxCalls int

	// Redact rewrites content before it leaves the process. Nil sends the
	// statement as-is.
	Redact func(string) string
}

// Evaluator classifies statements and turns verdicts into policy decisions.
//
// It implements policy.Evaluator, so it composes into a policy.Chain after
// the local rules and OPA. That ordering is what makes it affordable: a
// statement a free local rule already denies never reaches the model.
type Evaluator struct {
	cfg   Config
	cache *cache

	// prompt is the assembled system prompt: the rule's guidance (or the
	// default) plus the immutable output contract. Built once, because it
	// is identical for every statement this rule ever classifies.
	prompt string

	// promptKey fingerprints the prompt into the cache key. Without it, a
	// deploy that rewords the guidance would keep serving verdicts the OLD
	// prompt produced until the TTL expired, and an operator watching for
	// their change to take effect would see nothing.
	promptKey string

	calls  atomic.Int64
	denied atomic.Int64
	errs   atomic.Int64

	// budgetLogged ensures the "budget exhausted" line is logged once
	// rather than on every statement after the limit.
	budgetLogged sync.Once
	onBudget     func()
}

// New builds an Evaluator.
func New(cfg Config) (*Evaluator, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("hoopinspect/analyzer: no provider configured")
	}
	if cfg.Rule == "" {
		cfg.Rule = "ai_analysis"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxInputBytes <= 0 {
		cfg.MaxInputBytes = DefaultMaxInputBytes
	}
	for level, action := range cfg.Actions {
		if !level.Valid() {
			return nil, fmt.Errorf("hoopinspect/analyzer: unknown risk level %q", level)
		}
		if !action.Valid() {
			return nil, fmt.Errorf("hoopinspect/analyzer: unknown action %q for risk %q", action, level)
		}
	}
	prompt := BuildSystemPrompt(cfg.Guidance)
	return &Evaluator{
		cfg:       cfg,
		cache:     newCache(cfg.CacheSize, cfg.CacheTTL),
		prompt:    prompt,
		promptKey: fingerprint(prompt),
	}, nil
}

// Evaluate implements policy.Evaluator.
//
// The interface gives no context, so the deadline comes from the configured
// timeout. That matches how policy.OPAClient already behaves: the caller's
// cancellation does not reach here, and the timeout is what bounds a stalled
// provider's hold on the connection.
func (e *Evaluator) Evaluate(stmt hoopinspect.Statement) policy.Verdict {
	return e.EvaluateWith(stmt, nil)
}

// EvaluateWith implements policy.ContextualEvaluator.
//
// It reads one thing from the context and writes one. A gate-phase policy
// that asked for this source replaces the rule's configured trigger, which is
// what lets an operator move the "is this worth a model call" question out of
// YAML and into the Rego their InfoSec team already owns.
//
// The facts it establishes travel the other way, as a policy.Finding the
// Chain carries to whatever decides next. That hop is the entire connection
// between the model and OPA: neither has any idea the other exists.
func (e *Evaluator) EvaluateWith(stmt hoopinspect.Statement, ec *policy.EvalContext) policy.Verdict {
	res, status, err := e.classify(context.Background(), stmt, ec)
	if status == "" {
		// Not eligible: a response frame, a protocol with no builder.
		// Reporting would put a finding on rows where no analyzer could
		// ever have run, which reads as a failure rather than as absence.
		return policy.Verdict{}
	}

	level, action := RiskLevel(""), Action("")
	if status == StatusOK || status == StatusCached {
		level = res.RiskLevel
		action = e.cfg.Actions.actionFor(level)
	}
	e.report(ec, status, level, action)

	switch status {
	case StatusRefused:
		// send=refuse: the statement carries a detected entity and must
		// not be transmitted. Denying locally costs nothing and is the
		// only outcome consistent with the setting; allowing would leak
		// nothing but would also mean the operator's "refuse" did nothing.
		e.denied.Add(1)
		msg := e.cfg.Message
		if msg == "" {
			msg = "statement contains sensitive data and cannot be risk-analyzed"
		}
		v := policy.Deny(e.cfg.Rule, msg)
		v.Annotations = e.notes(status, "", string(ActionBlock))
		return v

	case StatusError:
		// failure() decides whether this denies. Either way the status
		// travels, so a decide-phase policy can refuse a statement the
		// model never saw instead of reading a missing level as low.
		v := e.failure(err)
		v.Annotations = e.notes(status, "", "")
		return v

	case StatusSkipped, StatusBudget:
		// Neither is a provider failure: the local rules and OPA still
		// ran and allowed this statement. Falling through to allow is
		// the same outcome as a lane with no analyzer, which is what a
		// spent budget and an unmatched trigger both mean.
		return policy.Verdict{Annotations: e.notes(status, "", "")}
	}

	// The risk level rides on every classified statement, allowed or not.
	// store.SessionRecord.RiskLevel folds these max-wins into a per-session
	// verdict, and a session whose risk only ever showed up on denials
	// would report clean right up until something got blocked.
	//
	// Title and explanation stay OUT of the trail: they are model prose,
	// audit redaction does not reach Metadata, and a model that quotes the
	// statement back would write the value into the record verbatim.
	notes := e.notes(status, string(level), string(action))

	if action != ActionBlock {
		// Allow, warn, defer and require_review all forward from HERE.
		// They differ in the record, and the last two differ in who
		// decides next: the level AND the action are on the finding, so a
		// decide-phase policy or the review gate sees them and answers.
		// Block is the only action this evaluator enforces itself.
		return policy.Verdict{Annotations: notes}
	}

	e.denied.Add(1)
	msg := e.cfg.Message
	if msg == "" {
		msg = res.Title
	}
	if msg == "" {
		msg = "refused by risk analysis"
	}
	v := policy.Deny(e.cfg.Rule, msg)
	v.Annotations = notes
	return v
}

// report writes this rule's outcome onto the shared context.
//
// The fold is the analyzer's own, not the Chain's, because only the analyzer
// knows how two of its verdicts combine: the most degraded status wins so a
// second rule that succeeded cannot hide the first one's outage, and among
// equally answered ones the HIGHEST risk wins so a rule rating a statement
// low cannot erase one that rated it high.
//
// A degraded fold carries NO level, whichever order the rules ran in. The
// alternative was tempting (one rule errored, another did say "high", so
// report both), but it makes the result depend on evaluation order and it
// breaks the invariant a policy relies on: Answered() false means there is
// nothing here to read. The level is not lost, because the risk_level
// ANNOTATION is a separate channel that keeps the highest seen; the audit
// record still shows the high while the policy is told that this source
// could not finish.
//
// The action rides along with the level, and breaks a TIE between two equally
// answered rules at the same level by keeping the stricter one. Without that
// tie-break the fold is first-writer-wins, so a lane with `high: warn` on one
// rule and `high: require_review` on another would silently drop the review
// gate whenever the warn rule happened to run first — enforcement decided by
// slice order, which is the failure this package refuses everywhere else.
func (e *Evaluator) report(ec *policy.EvalContext, status string, level RiskLevel, action Action) {
	if ec == nil {
		return
	}
	f := policy.Finding{
		Source: Source,
		Rule:   e.cfg.Rule,
		Status: findingStatus(status),
		Reason: statusReason(status),
	}
	if level != "" {
		f.Values = map[string]any{
			FindingRiskLevel: string(level),
			FindingAction:    string(action),
		}
	}

	if prev, ok := ec.Finding(Source); ok {
		switch {
		case foldRank(prev.Status) > foldRank(f.Status):
			f = prev
		case foldRank(prev.Status) == foldRank(f.Status):
			if keepPrevious(prev, level, action) {
				f = prev
			}
		}
		if !f.Answered() {
			f.Values = nil
		}
	}
	if ec.Findings == nil {
		ec.Findings = make(map[string]policy.Finding, 1)
	}
	ec.Findings[Source] = f
}

// foldRank orders statuses for folding THIS producer's own rules together.
//
// It differs from policy.FindingRank in one place, and the difference is the
// whole point: SKIPPED ranks BELOW answered here, where FindingRank puts it
// above.
//
// FindingRank orders by "how little the producer established", which is right
// for describing one finding, and policy.Finding.Merge deliberately keeps that
// ordering — its tests assert that a skip survives a later answer. This fold
// asks a different question. A rule whose trigger did not match has not failed,
// it did not APPLY, and letting it displace a rule that classified means a lane
// carrying two ai_analysis rules reports nothing whenever one of them is not
// interested.
//
// That failure is silent and it is why this exists: the statement is
// forwarded, the audit line still shows the risk level from the annotations,
// and only the finding — the channel the review gate and a decide-phase Rego
// read — comes back empty. A live HTTP lane hit exactly this.
//
// error and unavailable still outrank answered, so a rule that succeeded
// cannot hide another rule's outage. That was always what this fold was for;
// "did not apply" was never part of it.
func foldRank(status string) int {
	if status == policy.FindingSkipped {
		return 0
	}
	return policy.FindingRank(status)
}

// keepPrevious decides which of two equally answered findings survives:
// highest risk first, then strictest action.
func keepPrevious(prev policy.Finding, level RiskLevel, action Action) bool {
	if r := prevLevel(prev).rank(); r != level.rank() {
		return r > level.rank()
	}
	return prevAction(prev).actionRank() >= action.actionRank()
}

func prevLevel(f policy.Finding) RiskLevel {
	s, _ := f.Values[FindingRiskLevel].(string)
	return RiskLevel(s)
}

func prevAction(f policy.Finding) Action {
	s, _ := f.Values[FindingAction].(string)
	return Action(s)
}

// notes builds the annotation set for one evaluation.
//
// The rule name is always present so a lane with two ai_analysis rules can be
// read; level and action are present only where they mean something, because
// a risk_action beside no level describes a mapping nothing performed.
func (e *Evaluator) notes(status, level, action string) map[string]string {
	notes := map[string]string{
		MetadataAIStatus: status,
		MetadataAIRule:   e.cfg.Rule,
	}
	if level != "" {
		notes[MetadataRiskLevel] = level
	}
	if action != "" {
		notes[MetadataAction] = action
	}
	return notes
}

// wanted reports whether this statement should be classified.
//
// A gate-phase policy that named this source overrides the configured trigger
// in BOTH directions. Widening only would make the gate a second trigger
// ORed with the first, and an operator who moved the decision into Rego would
// find their YAML still forcing calls they told Rego to skip.
func (e *Evaluator) wanted(stmt hoopinspect.Statement, ec *policy.EvalContext) bool {
	if want, stated := ec.WantsRun(Source); stated {
		return want
	}
	return e.cfg.Trigger.Matches(stmt)
}

// classify runs the trigger, cache, budget and provider for one statement.
//
// It returns what happened as one of the policy.AIStatus values. Only ok and
// cached come with a usable Result; only error comes with a non-nil error. An
// empty status means the analyzer was never eligible to look at this
// statement, which is different from having looked and declined.
func (e *Evaluator) classify(
	ctx context.Context,
	stmt hoopinspect.Statement,
	ec *policy.EvalContext,
) (Result, string, error) {
	// Requests only. A response verdict cannot prevent anything: for a
	// write the damage is already done, and read-side exposure is masking's
	// job, which is cheaper and already runs.
	if stmt.Direction != hoopinspect.FromClient {
		return Result{}, "", nil
	}
	builder, ok := BuilderFor(stmt.Protocol)
	if !ok {
		return Result{}, "", nil
	}

	// The trigger runs before the builder, because building content
	// normalizes and truncates the whole statement and the common case on a
	// database lane is a trigger that does not match.
	if !e.wanted(stmt, ec) {
		return Result{}, StatusSkipped, nil
	}
	content, ok := builder.Build(stmt, e.cfg.MaxInputBytes)
	if !ok {
		return Result{}, StatusSkipped, nil
	}

	cacheKey := e.promptKey + ":" + content.CacheKey
	if cached, hit := e.cache.get(cacheKey); hit {
		return cached, StatusCached, nil
	}

	text := content.Text
	if e.cfg.Redact != nil {
		text = e.cfg.Redact(text)
		if text == RefuseSentinel {
			return Result{}, StatusRefused, nil
		}
	}
	// Reserve a slot atomically.
	//
	// One Evaluator is shared by every connection on the lane, so reading a
	// counter and incrementing it in two steps leaves a window where every
	// goroutine in flight reads the same under-budget value and every one of
	// them calls the provider. A budget of 100 becomes 100 plus however many
	// connections were concurrent, which is exactly the runaway the budget
	// exists to bound.
	//
	// Add returns the post-increment value, so exactly one goroutine can
	// observe each slot. An over-budget reservation is handed back, keeping
	// Stats.Calls a count of provider calls made rather than of attempts,
	// which is the number that tracks the bill.
	if n := e.calls.Add(1); e.cfg.MaxCalls > 0 && n > int64(e.cfg.MaxCalls) {
		e.calls.Add(-1)
		e.budgetLogged.Do(func() {
			if e.onBudget != nil {
				e.onBudget()
			}
		})
		return Result{}, StatusBudget, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()

	res, err := e.cfg.Provider.Classify(callCtx, e.prompt, text)
	if err != nil {
		e.errs.Add(1)
		return Result{}, StatusError, err
	}
	if res == nil || !res.RiskLevel.Valid() {
		e.errs.Add(1)
		return Result{}, StatusError, fmt.Errorf(
			"provider returned no usable risk level")
	}

	e.cache.put(cacheKey, *res)
	return *res, StatusOK, nil
}

// failure renders a classification error according to the fail mode.
//
// The shape matches policy.OPAClient.failure exactly: fail-open returns a
// non-denying verdict carrying Err, so policy.Chain accumulates the error
// instead of swallowing it, and the statement's audit record still shows that
// the analyzer could not answer.
func (e *Evaluator) failure(err error) policy.Verdict {
	if e.cfg.FailOpen {
		return policy.Verdict{Err: err}
	}
	return policy.Verdict{
		Denied:  true,
		Message: "risk analysis unavailable; denying",
		Rule:    e.cfg.Rule,
		Err:     err,
	}
}

// Stats reports what the analyzer has done. It backs the /stats admin
// endpoint, where an operator watches the hit rate before enforcing.
type Stats struct {
	Calls       int64  `json:"calls"`
	Denied      int64  `json:"denied"`
	Errors      int64  `json:"errors"`
	CacheHits   uint64 `json:"cache_hits"`
	CacheMisses uint64 `json:"cache_misses"`
}

// Stats returns a snapshot.
func (e *Evaluator) Stats() Stats {
	hits, misses := e.cache.stats()
	return Stats{
		Calls:       e.calls.Load(),
		Denied:      e.denied.Load(),
		Errors:      e.errs.Load(),
		CacheHits:   hits,
		CacheMisses: misses,
	}
}

// Rule reports the rule name this analyzer denies under.
func (e *Evaluator) Rule() string { return e.cfg.Rule }

// SystemPrompt returns the assembled prompt this Evaluator sends.
//
// Exported for tests and for an operator-facing report: the config file shows
// what was written, not what three levels of precedence resolved to.
func (e *Evaluator) SystemPrompt() string { return e.prompt }

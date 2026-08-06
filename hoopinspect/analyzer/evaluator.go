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

// matches reports whether stmt should be classified.
func (t Trigger) matches(stmt hoopinspect.Statement) bool {
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
		if action == ActionRequireReview {
			return nil, fmt.Errorf(
				"hoopinspect/analyzer: action %q is not supported by this build: "+
					"holding a statement for human approval needs a review backend, "+
					"and this relay has none", ActionRequireReview)
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
	res, verdict, ok := e.classify(context.Background(), stmt)
	if !ok {
		return verdict
	}

	action := e.cfg.Actions.actionFor(res.RiskLevel)

	// The risk level rides on every classified statement, allowed or not.
	// store.SessionRecord.RiskLevel folds these max-wins into a per-session
	// verdict, and a session whose risk only ever showed up on denials
	// would report clean right up until something got blocked.
	//
	// Title and explanation stay OUT of the trail: they are model prose,
	// audit redaction does not reach Metadata, and a model that quotes the
	// statement back would write the value into the record verbatim.
	notes := map[string]string{
		MetadataRiskLevel: string(res.RiskLevel),
		MetadataAction:    string(action),
	}

	if action != ActionBlock {
		// Allow and warn both forward. They differ only in the record.
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

// classify runs the trigger, cache, budget and provider for one statement.
//
// It returns ok=false with a ready-made verdict when the statement should not
// be classified at all (no trigger match, nothing to send) or when
// classification failed and the fail mode decided the outcome.
func (e *Evaluator) classify(ctx context.Context, stmt hoopinspect.Statement) (Result, policy.Verdict, bool) {
	// Requests only. A response verdict cannot prevent anything: for a
	// write the damage is already done, and read-side exposure is masking's
	// job, which is cheaper and already runs.
	if stmt.Direction != hoopinspect.FromClient {
		return Result{}, policy.Verdict{}, false
	}
	if !e.cfg.Trigger.matches(stmt) {
		return Result{}, policy.Verdict{}, false
	}

	builder, ok := BuilderFor(stmt.Protocol)
	if !ok {
		return Result{}, policy.Verdict{}, false
	}
	content, ok := builder.Build(stmt, e.cfg.MaxInputBytes)
	if !ok {
		return Result{}, policy.Verdict{}, false
	}

	cacheKey := e.promptKey + ":" + content.CacheKey
	if cached, hit := e.cache.get(cacheKey); hit {
		return cached, policy.Verdict{}, true
	}

	if e.cfg.MaxCalls > 0 && e.calls.Load() >= int64(e.cfg.MaxCalls) {
		e.budgetLogged.Do(func() {
			if e.onBudget != nil {
				e.onBudget()
			}
		})
		// Budget exhaustion is not a provider failure: the local rules
		// and OPA still ran and allowed this statement. Falling through
		// to allow is the same outcome as a lane with no analyzer, which
		// is what a spent budget means.
		return Result{}, policy.Verdict{}, false
	}

	text := content.Text
	if e.cfg.Redact != nil {
		text = e.cfg.Redact(text)
		if text == RefuseSentinel {
			// send=refuse: the statement carries a detected entity and
			// must not be transmitted. Denying locally costs nothing
			// and is the only outcome consistent with the setting —
			// allowing would leak nothing but would also mean the
			// operator's "refuse" did nothing.
			e.denied.Add(1)
			msg := e.cfg.Message
			if msg == "" {
				msg = "statement contains sensitive data and cannot be risk-analyzed"
			}
			v := policy.Deny(e.cfg.Rule, msg)
			v.Annotations = map[string]string{MetadataAction: string(ActionBlock)}
			return Result{}, v, false
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()

	e.calls.Add(1)
	res, err := e.cfg.Provider.Classify(callCtx, e.prompt, text)
	if err != nil {
		e.errs.Add(1)
		return Result{}, e.failure(err), false
	}
	if res == nil || !res.RiskLevel.Valid() {
		e.errs.Add(1)
		return Result{}, e.failure(fmt.Errorf(
			"provider returned no usable risk level")), false
	}

	e.cache.put(cacheKey, *res)
	return *res, policy.Verdict{}, true
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

package sidecar

import (
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoopinspect/analyzer"
	"github.com/hoophq/hoopinspect/policy"
	"github.com/hoophq/hoopinspect/review"
)

// ReviewConfig configures the human-approval gate for the whole process.
//
// One gateway serves every lane, for the same reason one analyzer provider
// does: per-lane variation lives in the rules, and a second credential per
// lane would double the secret surface for a case nobody has asked for.
//
// There is no broker or backend selector. The sidecar runs beside Envoy and
// everything it needs from the control plane it gets over the gateway's HTTPS
// API — one deployment shape, one transport — so naming the implementation in
// config would be offering the operator a choice they do not have.
type ReviewConfig struct {
	// APIURL is the hoop gateway root, e.g. https://gateway.hoop.internal.
	// Not the /api prefix: the client appends its own paths.
	APIURL string `json:"api_url"`

	// TokenFile is the path to the sandbox's hpk_ credential.
	//
	// A PATH, never the material, matching how the analyzer credential is
	// already handled. The file must not be readable by group or other, and
	// the process reads it once at startup.
	TokenFile string `json:"token_file"`

	// TimeoutSec bounds one gateway call. Zero uses review.DefaultTimeout.
	//
	// Keep it short. The call sits inline on a proxied connection, so the
	// same warning the analyzer carries applies: a slow inline call can
	// outlive an upstream's keep-alive, here at a smaller magnitude than a
	// model call.
	TimeoutSec int `json:"timeout_sec,omitempty"`

	// RequireMarker refuses a gated statement carrying no
	// x-hoop-correlation-id marker, instead of filing a review for it.
	//
	// Off by default. Turn it on for a busy lane: the create path dedupes on
	// the marker, so without one every attempt is a new request and a
	// polling agent fills the queue with duplicates of one question.
	RequireMarker bool `json:"require_marker,omitempty"`

	// PollCacheTTLSec caches a PENDING answer for this many seconds, so an
	// agent re-issuing in a tight loop does not turn one refusal into a
	// stream of gateway calls. Zero disables it, matching every other cache
	// in this config.
	//
	// NEGATIVE answers only. An APPROVED answer is never cached, because a
	// cached approval is a revocation that cannot be honored. Caching a
	// refusal only delays an approval taking effect by at most this long,
	// which is the safe direction — but keep it to seconds.
	PollCacheTTLSec int `json:"poll_cache_ttl_sec,omitempty"`
}

// validate checks the review section in isolation.
func (r *ReviewConfig) validate() []string {
	if r == nil {
		return nil
	}
	var problems []string

	if err := review.ValidateAPIURL(r.APIURL); err != nil {
		problems = append(problems, "review: "+err.Error())
	}
	if r.TokenFile == "" {
		problems = append(problems, "review: no token_file set; the hpk_ credential is named by path, never inlined")
	}
	// Negative is refused rather than clamped, for the same reason the
	// analyzer's bounds are: zero means "off" or "default" everywhere here,
	// so a typo'd minus sign would silently remove a ceiling that looks set.
	if r.TimeoutSec < 0 {
		problems = append(problems, "review: timeout_sec is negative")
	}
	if r.PollCacheTTLSec < 0 {
		problems = append(problems, "review: poll_cache_ttl_sec is negative")
	}
	return problems
}

func (r *ReviewConfig) timeout() time.Duration {
	return time.Duration(r.TimeoutSec) * time.Second
}

// reviewDeps carries the process-wide review client to each lane's policy
// build. One client serves every lane, so the credential is read once.
type reviewDeps struct {
	cfg    *ReviewConfig
	client *review.Client
}

// setupReview builds the process-wide review client from the config.
//
// Returns (nil, nil) when no review section is present, which is every
// deployment that does not gate on human approval.
func setupReview(cfg *Config) (*reviewDeps, error) {
	if cfg == nil || cfg.Review == nil {
		return nil, nil
	}
	if problems := cfg.Review.validate(); len(problems) > 0 {
		return nil, fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	// Same reader as the analyzer credential, so a 0644 token fails at
	// startup with the mode in the message rather than being quietly used.
	secret, err := analyzer.ReadSecretFile(cfg.Review.TokenFile)
	if err != nil {
		return nil, err
	}
	client, err := review.NewClient(cfg.Review.APIURL, string(secret.Bytes()), cfg.Review.timeout())
	if err != nil {
		return nil, err
	}
	return &reviewDeps{cfg: cfg.Review, client: client}, nil
}

// reviewRules reports which ai_analysis rules on a lane gate on human
// approval, and the triggers that select the statements they could gate.
//
// The triggers are copied so the CLAIM phase can narrow to the same set of
// statements the analyzer would classify. Without that it would spend a
// gateway round-trip on every query the lane carries.
//
// A rule with an EMPTY trigger contributes an empty Trigger, which matches
// nothing on its own — correct, because the only configuration that allows an
// empty trigger is a lane with opa.gate on, and there the policy states the
// answer through EvalContext.Requested, which the claim phase reads first.
func reviewRules(rules []policy.Rule) (triggers []analyzer.Trigger, used bool) {
	for _, r := range rules {
		if r.Type != policy.MatchAIAnalysis {
			continue
		}
		if !ruleWantsReview(r) {
			continue
		}
		used = true
		triggers = append(triggers, triggerFrom(r.Trigger))
	}
	return triggers, used
}

// ruleWantsReview reports whether any risk level on this rule maps to
// require_review.
func ruleWantsReview(r policy.Rule) bool {
	for _, raw := range [...]string{r.HighRisk, r.MediumRisk, r.LowRisk} {
		if analyzer.Action(raw) == analyzer.ActionRequireReview {
			return true
		}
	}
	return false
}

// anyReviewRule reports whether ANY listener in the config gates on human
// approval, so an unused review section can be refused.
func (c *Config) anyReviewRule() bool {
	for _, lc := range c.Listeners {
		pc, _ := c.resolve(lc)
		if _, used := reviewRules(pc.Rules); used {
			return true
		}
	}
	return false
}

// buildReviewGate assembles a lane's review gate, or returns nil when the lane
// does not gate on approval.
func buildReviewGate(pc PolicyConfig, rd *reviewDeps) (*review.Gate, error) {
	triggers, used := reviewRules(pc.Rules)
	if !used {
		return nil, nil
	}
	if rd == nil || rd.client == nil {
		// Unreachable from a config file — validateReviewRules refuses this
		// combination — but a caller building a Config by hand reaches it,
		// and forwarding a statement an operator asked to gate is the one
		// outcome that must not happen quietly.
		return nil, fmt.Errorf(
			"a rule gates on %q and the config has no \"review\" section to reach a gateway with",
			analyzer.ActionRequireReview)
	}
	return review.New(review.Options{
		Client:        rd.client,
		Triggers:      triggers,
		RequireMarker: rd.cfg.RequireMarker,
		PendingTTL:    time.Duration(rd.cfg.PollCacheTTLSec) * time.Second,
	})
}

// validateReviewRules refuses a lane whose review gate could not work.
//
// Every refusal here is a control that would otherwise load and forward
// everything, which is worse than a startup error: an operator would read
// `high: require_review` in their config and believe statements are being held
// for a person.
func validateReviewRules(pc PolicyConfig, lc ListenerConfig, cfg *ReviewConfig, lane string) []string {
	_, used := reviewRules(pc.Rules)
	if !used {
		return nil
	}
	var problems []string

	if cfg == nil {
		problems = append(problems, fmt.Sprintf(
			"%s: has a rule that gates on %q but the config has no \"review\" section; "+
				"add one with api_url and token_file, or use block, warn or defer",
			lane, analyzer.ActionRequireReview))
	}

	// The gateway scopes an approval by connection: a sandbox may reach
	// several, and an approval for appdb must not authorize the same SQL
	// against payments-db. A lane with no connection name cannot say which
	// one it is, and the gate would refuse every gated statement at runtime.
	if lc.Connection == "" {
		problems = append(problems, fmt.Sprintf(
			"%s: gates on %q but sets no \"connection\"; an approval is scoped to a "+
				"connection, so the lane has to name the one it fronts",
			lane, analyzer.ActionRequireReview))
	}
	return problems
}

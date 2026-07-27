package controller

import (
	"context"
	"sync/atomic"

	libhoopaianalyzer "libhoop/aianalyzer"

	aianalyzer "github.com/hoophq/hoop/common/aianalyzer"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
)

// httpAnalyzer adapts the shared common/aianalyzer engine to the contract
// libhoop's HTTP proxy expects. libhoop cannot import common, so the agent owns
// this bridge: it runs the engine, encodes the per-request verdict (stamped with
// a monotonic sequence and the connection id so the gateway dedupes the sticky
// response spec), and renders the 403 body for blocked requests.
type httpAnalyzer struct {
	engine aianalyzer.Analyzer
	connID string
	// seq is a monotonic per-connection counter stamped on each emitted verdict.
	// The gateway uses it to dedupe the verdict, which is sticky on the shared
	// response spec and repeats across response chunks.
	seq atomic.Uint64
}

// newHTTPAnalyzer builds the engine from the gateway-resolved config and wraps
// it. It returns an error only when the provider configuration is invalid;
// callers fail open (forward without analysis) on error.
func newHTTPAnalyzer(cfg *pb.AISessionAnalyzerParams, connID string) (libhoopaianalyzer.Analyzer, error) {
	engine, err := aianalyzer.NewClient(
		aianalyzer.ProviderConfig{
			Provider: cfg.Provider,
			APIURL:   ptrIfNonEmpty(cfg.APIURL),
			APIKey:   ptrIfNonEmpty(cfg.APIKey),
			Model:    cfg.Model,
		},
		aianalyzer.RuleConfig{
			Name:             cfg.RuleName,
			CustomPrompt:     cfg.CustomPrompt,
			LowRiskAction:    aianalyzer.Action(cfg.LowRiskAction),
			MediumRiskAction: aianalyzer.Action(cfg.MediumRiskAction),
			HighRiskAction:   aianalyzer.Action(cfg.HighRiskAction),
		},
	)
	if err != nil {
		return nil, err
	}
	return &httpAnalyzer{engine: engine, connID: connID}, nil
}

// AnalyzeRequest classifies a single request and translates the engine decision
// into libhoop's enforcement Result. It is fail-open at the libhoop boundary:
// engine errors propagate so the proxy logs and forwards the request, but the
// returned Result still carries the spec key so the proxy can clear any stale
// verdict from the shared response spec.
func (a *httpAnalyzer) AnalyzeRequest(ctx context.Context, method, target string, body []byte) (*libhoopaianalyzer.Result, error) {
	res := &libhoopaianalyzer.Result{SpecKey: aianalyzer.VerdictInfoKey}

	decision, err := a.engine.AnalyzeRequest(ctx, method, target, body)
	if err != nil {
		return res, err
	}
	if decision == nil {
		return res, nil
	}

	verdict, err := decision.Verdict(a.seq.Add(1), a.connID).Encode()
	if err != nil {
		log.With("conn", a.connID).Warnf("failed encoding ai analyzer verdict: %v", err)
		return res, nil
	}
	res.Verdict = verdict

	log.With("conn", a.connID).Infof("ai session analyzer verdict, outcome=%s risk=%s rule=%q title=%q",
		decision.Outcome, decision.RiskLevel, decision.RuleName, decision.Title)

	if decision.Outcome == aianalyzer.OutcomeBlock {
		res.Block = true
		res.BlockBody = aianalyzer.RenderForbidden(decision)
	}
	return res, nil
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

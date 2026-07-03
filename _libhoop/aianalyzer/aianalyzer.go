// Package aianalyzer is the injected contract for the agent's HTTP proxy AI
// session analyzer.
//
// This is the OSS mirror of libhoop/aianalyzer. The contract is identical in
// both builds because the agent (shared code) injects an implementation of
// Analyzer into NewHttpProxy. The classification engine lives in
// github.com/hoophq/hoop/common and ships in both builds; this package carries
// only the contract — no engine, no LLM SDKs.
package aianalyzer

import "context"

// Analyzer is the per-request HTTP analyzer the proxy consults inline. The agent
// supplies the implementation (an adapter over common/aianalyzer); a nil
// Analyzer disables analysis for the connection.
type Analyzer interface {
	AnalyzeRequest(ctx context.Context, method, target string, body []byte) (*Result, error)
}

// Result is the enforcement outcome for a single request, fully prepared by the
// injected analyzer so libhoop performs no AI- or HTTP-response-specific logic.
type Result struct {
	// SpecKey is the packet spec key for the verdict. It is always set so the
	// proxy can record the verdict or clear a stale one on the shared response
	// spec, which is sticky across a connection's requests.
	SpecKey string
	// Verdict is the encoded verdict bytes to attach under SpecKey. An empty
	// Verdict clears any previously attached verdict.
	Verdict []byte
	// Block rejects this single request when true; the session stays open.
	Block bool
	// BlockBody is the response written to the client when Block is true.
	BlockBody []byte
}

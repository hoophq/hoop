package spectypes

import "github.com/vmihailenco/msgpack/v5"

const (
	DataMaskingInfoKey = "datamasking.info"

	// AIAnalyzerInfoKey is the packet spec key carrying the per-request AI
	// session analyzer verdict from the agent to the gateway audit plugin. The
	// wire layout mirrors common/aianalyzer.Verdict by value so the audit plugin
	// decodes it without importing the engine package.
	AIAnalyzerInfoKey = "aianalyzer.info"
)

// AIAnalyzerVerdict is the decoded per-request analyzer result attached to
// HTTP proxy response packets. It mirrors common/aianalyzer.Verdict.
//
// Seq and ConnID identify the originating request: the verdict is sticky on the
// shared response spec and therefore repeats across every response chunk of a
// request. Consumers dedupe per (ConnID, Seq) — a monotonic, per-connection
// request counter — to count each analyzed request exactly once.
type AIAnalyzerVerdict struct {
	Outcome     string `msgpack:"outcome"`
	RiskLevel   string `msgpack:"risk_level"`
	Title       string `msgpack:"title"`
	Explanation string `msgpack:"explanation"`
	RuleName    string `msgpack:"rule_name"`
	Seq         uint64 `msgpack:"seq"`
	ConnID      string `msgpack:"conn_id"`
}

// DecodeAIAnalyzerVerdict decodes a msgpack-encoded analyzer verdict.
func DecodeAIAnalyzerVerdict(data []byte) (*AIAnalyzerVerdict, error) {
	var v AIAnalyzerVerdict
	return &v, msgpack.Unmarshal(data, &v)
}

type TransformationSummary struct {
	InfoType string          `msgpack:"info_type"`
	Field    string          `msgpack:"field"`
	Results  []SummaryResult `msgpack:"results"`
}

type SummaryResult struct {
	Count   int64  `msgpack:"count"`
	Code    string `msgpack:"code"`
	Details string `msgpack:"details"`
}

type TransformationOverview struct {
	TransformedBytes int64                   `msgpack:"transformed_bytes"`
	Summaries        []TransformationSummary `msgpack:"transformation_summary"`
	Err              error                   `msgpack:"err"`
}

type DataMaskingInfo struct {
	Items []*TransformationOverview `msgpack:"items"`
}

func (r *DataMaskingInfo) Encode() ([]byte, error) {
	return msgpack.Marshal(r)
}

func Decode(data []byte) (*DataMaskingInfo, error) {
	var info DataMaskingInfo
	return &info, msgpack.Unmarshal(data, &info)
}

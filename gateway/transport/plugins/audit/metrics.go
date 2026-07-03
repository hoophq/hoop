package audit

import (
	"encoding/json"
	"strings"
)

type DataMaskingMetric struct {
	InfoTypes        map[string]int64 `json:"info_types"`
	TotalRedactCount int64            `json:"total_redact_count"`
	TransformedBytes int64            `json:"transformed_bytes"`
	ErrCount         int              `json:"err_count"`
}

// AIAnalyzerVerdictMetric is the highest-risk per-request verdict observed
// during a session, retained so the session record carries a representative
// example alongside the aggregate counts.
type AIAnalyzerVerdictMetric struct {
	Outcome     string `json:"outcome"`
	RiskLevel   string `json:"risk_level"`
	Title       string `json:"title"`
	Explanation string `json:"explanation"`
	RuleName    string `json:"rule_name"`
}

// AIAnalyzerMetric aggregates the per-request AI session analyzer verdicts of an
// HTTP proxy session. Unlike the exec path (one verdict per session), an HTTP
// session produces one verdict per request; this folds them into counts plus
// the worst verdict seen.
type AIAnalyzerMetric struct {
	TotalRequests int64                    `json:"total_requests"`
	RiskCounts    map[string]int64         `json:"risk_counts"`
	OutcomeCounts map[string]int64         `json:"outcome_counts"`
	BlockedCount  int64                    `json:"blocked_count"`
	Worst         *AIAnalyzerVerdictMetric `json:"worst,omitempty"`
}

type SessionMetric struct {
	DataMasking *DataMaskingMetric `json:"data_masking"`
	AIAnalyzer  *AIAnalyzerMetric  `json:"ai_analyzer,omitempty"`
	EventSize   int64              `json:"event_size"`
	Truncated   bool               `json:"truncated"`
}

func (s *SessionMetric) addInfoType(key string, size int64) {
	if s.DataMasking.InfoTypes != nil {
		s.DataMasking.InfoTypes[key] += size
	}
}

// merge folds another metric's DLP counters into this one. Used to accumulate
// per-flush window metrics into the cumulative session total. Both receiver and
// argument come from newSessionMetric, so DataMasking is always initialized.
func (s *SessionMetric) merge(other SessionMetric) {
	s.DataMasking.TotalRedactCount += other.DataMasking.TotalRedactCount
	s.DataMasking.TransformedBytes += other.DataMasking.TransformedBytes
	s.DataMasking.ErrCount += other.DataMasking.ErrCount
	for k, v := range other.DataMasking.InfoTypes {
		s.DataMasking.InfoTypes[k] += v
	}

	if other.AIAnalyzer != nil {
		s.ensureAIAnalyzer().mergeFrom(other.AIAnalyzer)
	}
}

// ensureAIAnalyzer lazily initializes the AI analyzer metric. It stays nil for
// sessions without AI analysis so the metrics blob omits the field entirely.
func (s *SessionMetric) ensureAIAnalyzer() *AIAnalyzerMetric {
	if s.AIAnalyzer == nil {
		s.AIAnalyzer = &AIAnalyzerMetric{
			RiskCounts:    map[string]int64{},
			OutcomeCounts: map[string]int64{},
		}
	}
	return s.AIAnalyzer
}

// foldVerdict accumulates a single per-request analyzer verdict into the metric,
// retaining the highest-risk verdict as the representative example.
func (m *AIAnalyzerMetric) foldVerdict(outcome, riskLevel, title, explanation, ruleName string) {
	m.TotalRequests++
	if riskLevel != "" {
		m.RiskCounts[strings.ToLower(riskLevel)]++
	}
	if outcome != "" {
		m.OutcomeCounts[strings.ToLower(outcome)]++
	}
	if strings.EqualFold(outcome, "block") {
		m.BlockedCount++
	}
	if m.Worst == nil || riskRank(riskLevel) > riskRank(m.Worst.RiskLevel) {
		m.Worst = &AIAnalyzerVerdictMetric{
			Outcome:     outcome,
			RiskLevel:   riskLevel,
			Title:       title,
			Explanation: explanation,
			RuleName:    ruleName,
		}
	}
}

// mergeFrom folds another AI analyzer metric (a flush window) into this one.
func (m *AIAnalyzerMetric) mergeFrom(other *AIAnalyzerMetric) {
	m.TotalRequests += other.TotalRequests
	m.BlockedCount += other.BlockedCount
	for k, v := range other.RiskCounts {
		m.RiskCounts[k] += v
	}
	for k, v := range other.OutcomeCounts {
		m.OutcomeCounts[k] += v
	}
	if other.Worst != nil && (m.Worst == nil || riskRank(other.Worst.RiskLevel) > riskRank(m.Worst.RiskLevel)) {
		m.Worst = other.Worst
	}
}

// riskRank orders risk levels so the worst verdict can be selected. Unknown
// levels rank lowest.
func riskRank(level string) int {
	switch strings.ToLower(level) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func (s *SessionMetric) toMap() (map[string]any, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(data, &out)
}

func newSessionMetric() SessionMetric {
	return SessionMetric{DataMasking: &DataMaskingMetric{
		InfoTypes: map[string]int64{},
	}}
}

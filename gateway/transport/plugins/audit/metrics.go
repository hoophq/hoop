package audit

import "encoding/json"

type DataMaskingMetric struct {
	InfoTypes        map[string]int64 `json:"info_types"`
	TotalRedactCount int64            `json:"total_redact_count"`
	TransformedBytes int64            `json:"transformed_bytes"`
	ErrCount         int              `json:"err_count"`
}

type SessionMetric struct {
	DataMasking *DataMaskingMetric `json:"data_masking"`
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

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
}

func (s *SessionMetric) addInfoType(key string, size int64) {
	if s.DataMasking.InfoTypes != nil {
		s.DataMasking.InfoTypes[key] += size
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

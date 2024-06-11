package spectypes

import "github.com/vmihailenco/msgpack/v5"

const (
	DataMaskingInfoKey = "datamasking.info"
)

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

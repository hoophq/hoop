package dlp

import "fmt"

type (
	TransformationSummary struct {
		Index int
		Err   error
		// [info-type, transformed-bytes]
		Summary []string
		// [[count, code, details] ...]
		SummaryResult [][]string
	}
)

func (t *TransformationSummary) String() string {
	if len(t.Summary) == 2 {
		return fmt.Sprintf("chunk:%v, infotype:%v, transformedbytes:%v, result:%v",
			t.Index, t.Summary[0], t.Summary[1], t.SummaryResult)
	}
	if t.Err != nil {
		return fmt.Sprintf("chunk:%v, err:%v", t.Index, t.Err)
	}
	return ""
}

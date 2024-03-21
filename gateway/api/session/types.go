package sessionapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

const (
	ReviewTypeJit     = "jit"
	ReviewTypeOneTime = "onetime"
)

var (
	downloadTokenStore        = memory.New()
	defaultDownloadExpireTime = time.Minute * 5
)

type sessionParseOption struct {
	withLineBreak bool
	withEventTime bool
	withJsonFmt   bool
	withCsvFmt    bool
	events        []string
}

func WithOption(optKey types.SessionOptionKey, val any) *types.SessionOption {
	return &types.SessionOption{OptionKey: optKey, OptionVal: val}
}

func parseSessionToFile(s *types.Session, opts sessionParseOption) (output []byte) {
	var jsonEventStreamList []map[string]string
	for _, eventList := range s.EventStream {
		event := eventList.(types.SessionEventStream)
		eventTime, _ := event[0].(float64)
		eventType, _ := event[1].(string)
		eventData, _ := base64.StdEncoding.DecodeString(event[2].(string))
		if !slices.Contains(opts.events, eventType) {
			continue
		}
		if opts.withJsonFmt {
			jsonEventStreamList = append(jsonEventStreamList, map[string]string{
				"time":   s.StartSession.Add(time.Second * time.Duration(eventTime)).Format(time.RFC3339),
				"type":   eventType,
				"stream": string(eventData),
			})
			continue
		}
		if opts.withEventTime {
			eventTime := s.StartSession.Add(time.Second * time.Duration(eventTime)).Format(time.RFC3339)
			eventTime = fmt.Sprintf("%v ", eventTime)
			output = append(output, []byte(eventTime)...)
		}
		switch eventType {
		case "i":
			output = append(output, eventData...)
		case "o", "e":
			output = append(output, eventData...)
		}
		if opts.withLineBreak {
			output = append(output, '\n')
		}
		if opts.withCsvFmt {
			output = bytes.ReplaceAll(output, []byte("\t"), []byte(`,`))
		}
	}
	if opts.withJsonFmt {
		output, _ = json.Marshal(jsonEventStreamList)
	}
	return
}

package pgrest

import (
	"fmt"
	"time"

	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func (a *Agent) GetMeta(key string) (v string) {
	if len(a.Metadata) > 0 {
		if val, ok := a.Metadata[key]; ok {
			return val
		}
	}
	return
}

// TODO: add a custom json decoder to handle time.Time
func (s *Session) GetCreatedAt() (t time.Time) {
	t, _ = time.ParseInLocation("2006-01-02T15:04:05", s.CreatedAt, time.UTC)
	return
}

// TODO: add a custom json decoder to handle time.Time
func (s *Session) GetEndedAt() (t *time.Time) {
	if s.EndedAt != nil {
		endedAt, err := time.ParseInLocation("2006-01-02T15:04:05", *s.EndedAt, time.UTC)
		if err != nil {
			return
		}
		return &endedAt
	}
	return
}

func (s *Session) GetBlobInput() (data string) {
	if s.BlobInput != nil && len(s.BlobInput.BlobStream) > 0 {
		data, _ = s.BlobInput.BlobStream[0].(string)
	}
	return
}

func (s *Session) GetBlobStream() (events []types.SessionEventStream, size int64) {
	if s.BlobStream != nil && len(s.BlobStream.BlobStream) > 0 {
		for _, bs := range s.BlobStream.BlobStream {
			event, ok := bs.([]any)
			if !ok {
				return
			}
			events = append(events, event)
		}
		size = s.BlobStream.Size
	}
	return
}

func (s *Session) GetRedactCount() (count int64) {
	if s.Metadata["redact_count"] != nil {
		count, _ = s.Metadata["redact_count"].(int64)
	}
	return
}

func (s *ProxyManagerState) GetConnectedAt() (t time.Time) {
	t, _ = time.ParseInLocation("2006-01-02T15:04:05", s.ConnectedAt, time.UTC)
	return
}

func CoerceToAnyMap(src map[string]string) map[string]any {
	dst := map[string]any{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func CoerceToMapString(src map[string]any) map[string]string {
	dst := map[string]string{}
	for k, v := range src {
		dst[k] = fmt.Sprintf("%v", v)
	}
	return dst
}

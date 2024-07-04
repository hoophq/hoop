package eventlog

import eventlogv0 "github.com/hoophq/hoop/gateway/session/eventlog/v0"

type Encoder interface {
	Encode() ([]byte, error)
}

// DecodeLatest decodes the latest event log version.
// This function should be able to normalize old versions
// keeping old attribute defaults compatible.
func DecodeLatest(data []byte) (ev *eventlogv0.EventLog, err error) {
	return eventlogv0.Decode(data)
}

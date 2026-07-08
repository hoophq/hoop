package audit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
)

func TestTruncateOpaqueEventStream(t *testing.T) {
	p := &auditPlugin{}
	small := bytes.Repeat([]byte{0xAB}, 100)
	big := bytes.Repeat([]byte{0xCD}, maxOpaqueEventStreamBytes*3)

	tests := []struct {
		name     string
		payload  []byte
		connType pb.ConnectionType
		wantLen  int
	}{
		{"tcp small passes through", small, pb.ConnectionTypeTCP, len(small)},
		{"tcp large truncated", big, pb.ConnectionTypeTCP, maxOpaqueEventStreamBytes},
		{"rdp large truncated", big, pb.ConnectionTypeRDP, maxOpaqueEventStreamBytes},
		{"rdp small passes through", small, pb.ConnectionTypeRDP, len(small)},
		{"postgres large untouched", big, pb.ConnectionTypePostgres, len(big)},
		{"ssh large untouched", big, pb.ConnectionTypeSSH, len(big)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.truncateOpaqueEventStream(tt.payload, tt.connType)
			if len(got) != tt.wantLen {
				t.Fatalf("len=%d want=%d", len(got), tt.wantLen)
			}
			// Truncation must be a prefix, never a rewrite.
			if !bytes.Equal(got, tt.payload[:len(got)]) {
				t.Fatal("truncated stream is not a prefix of the original payload")
			}
		})
	}
}

// TestEncodeEventEntryTruncatesOpaqueProtocols exercises the full encode path
// (not just the truncation helper): large opaque payloads must land in the
// JSON triple as a base64 of the truncated prefix, while payloads of
// non-opaque protocols are stored whole.
func TestEncodeEventEntryTruncatesOpaqueProtocols(t *testing.T) {
	p := &auditPlugin{}
	startDate := time.Now().UTC()
	payload := bytes.Repeat([]byte{0xEF}, maxOpaqueEventStreamBytes*2)

	tests := []struct {
		name     string
		connType pb.ConnectionType
		wantLen  int
	}{
		{"rdp truncated at encode", pb.ConnectionTypeRDP, maxOpaqueEventStreamBytes},
		{"tcp truncated at encode", pb.ConnectionTypeTCP, maxOpaqueEventStreamBytes},
		{"postgres stored whole", pb.ConnectionTypePostgres, len(payload)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := eventlogv1.New(startDate.Add(time.Second), eventlogv1.OutputType, payload, nil)
			metrics := newSessionMetric()

			var buf strings.Builder
			p.encodeEventEntry(&buf, ev, &startDate, tt.connType, &metrics)

			var triple [3]any
			if err := json.Unmarshal([]byte(buf.String()), &triple); err != nil {
				t.Fatalf("encoded entry is not a valid JSON triple: %v", err)
			}
			b64, ok := triple[2].(string)
			if !ok {
				t.Fatalf("third element is not a string: %T", triple[2])
			}
			decoded, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded) != tt.wantLen {
				t.Fatalf("persisted payload len=%d want=%d", len(decoded), tt.wantLen)
			}
			if !bytes.Equal(decoded, payload[:tt.wantLen]) {
				t.Fatal("persisted payload is not a prefix of the original")
			}
		})
	}
}

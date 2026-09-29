package sessionapi

import (
	"testing"

	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

func TestRecordingFormat(t *testing.T) {
	for _, tt := range []struct {
		connType, subtype, verb string
		want                    openapi.SessionRecordingFormat
	}{
		{"custom", "", proto.ClientVerbConnect, openapi.SessionRecordingFormatPTY},
		{"command-line", "", proto.ClientVerbConnect, openapi.SessionRecordingFormatPTY},
		{"application", "python", proto.ClientVerbConnect, openapi.SessionRecordingFormatPTY},
		{"custom", "", proto.ClientVerbExec, openapi.SessionRecordingFormatExec},
		{"database", "postgres", proto.ClientVerbExec, openapi.SessionRecordingFormatExec},
		// custom connections whose client runs a protocol proxy, not a PTY
		{"custom", "kubernetes", proto.ClientVerbConnect, openapi.SessionRecordingFormatRaw},
		{"custom", "kubernetes-eks", proto.ClientVerbConnect, openapi.SessionRecordingFormatRaw},
		{"custom", "kubernetes-token", proto.ClientVerbConnect, openapi.SessionRecordingFormatRaw},
		{"custom", "httpproxy", proto.ClientVerbConnect, openapi.SessionRecordingFormatRaw},
		{"custom", "aws-ssm", proto.ClientVerbConnect, openapi.SessionRecordingFormatRaw},
		{"application", "ssh", proto.ClientVerbConnect, openapi.SessionRecordingFormatRaw},
		{"application", "tcp", proto.ClientVerbConnect, openapi.SessionRecordingFormatRaw},
		{"database", "postgres", proto.ClientVerbConnect, openapi.SessionRecordingFormatRaw},
		{"custom", "rdp", proto.ClientVerbConnect, openapi.SessionRecordingFormatRDP},
	} {
		s := &models.Session{ConnectionType: tt.connType, ConnectionSubtype: tt.subtype, Verb: tt.verb}
		if got := recordingFormat(s); got != tt.want {
			t.Errorf("recordingFormat(%s/%s, %s) = %q, want %q", tt.connType, tt.subtype, tt.verb, got, tt.want)
		}
	}
}

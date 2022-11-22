package dlp

import (
	"bytes"
	"testing"

	"github.com/runopsio/hoop/agent/pg"
	pgtypes "github.com/runopsio/hoop/common/pg"
)

type fakeResponseWriter struct {
	t           *testing.T
	dataRowsBuf *bytes.Buffer
	executed    bool
}

func (w *fakeResponseWriter) Close() error { return nil }
func (w *fakeResponseWriter) Write(got []byte) (int, error) {
	expected := w.dataRowsBuf.Bytes()
	if !bytes.Equal(expected, got) {
		w.t.Errorf("data row packet differs , got=%X, expected=%X", got, expected)
	}
	w.executed = true
	return 0, nil
}

func TestRedacttMiddleware(t *testing.T) {
	// test when dlpclient is nil and with empty info types
	for _, tt := range []struct {
		msg                      string
		client                   Client
		responseWriter           *fakeResponseWriter
		shouldCallNextMiddleware bool
		shouldCallResponseWriter bool
		maxPacketSize            int
		dataRowPackets           []*pg.Packet
	}{
		{
			msg:                      "it should buffer all data rows and redact it matching with the input",
			client:                   &fakeClient{},
			shouldCallNextMiddleware: false,
			shouldCallResponseWriter: true,
			dataRowPackets: []*pg.Packet{
				pg.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
				pg.NewDataRowPacket(3, "clara.shaw@sakilacustomer.org", "148", "2006-02-15 04:57:20"),
				pg.NewDataRowPacket(3, "danny.isom@sakilacustomer.org", "404", "2006-02-15 04:57:20"),
				pg.NewPacketWithType(pgtypes.ServerReadyForQuery),
			},
			responseWriter: &fakeResponseWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:                      "it should not redact it because of the missing server ready packet",
			client:                   &fakeClient{},
			shouldCallNextMiddleware: false,
			shouldCallResponseWriter: false,
			dataRowPackets: []*pg.Packet{
				pg.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
				pg.NewDataRowPacket(3, "clara.shaw@sakilacustomer.org", "148", "2006-02-15 04:57:20"),
				pg.NewDataRowPacket(3, "danny.isom@sakilacustomer.org", "404", "2006-02-15 04:57:20"),
			},
			responseWriter: &fakeResponseWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:                      "it should redact it because it reached max packet size",
			client:                   &fakeClient{},
			shouldCallNextMiddleware: false,
			shouldCallResponseWriter: true,
			maxPacketSize:            10,
			dataRowPackets: []*pg.Packet{
				pg.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
				pg.NewDataRowPacket(3, "clara.shaw@sakilacustomer.org", "148", "2006-02-15 04:57:20"),
				pg.NewDataRowPacket(3, "danny.isom@sakilacustomer.org", "404", "2006-02-15 04:57:20"),
			},
			responseWriter: &fakeResponseWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:                      "it should call next middleware because it's not a data row packet",
			client:                   &fakeClient{},
			shouldCallNextMiddleware: true,
			shouldCallResponseWriter: false,
			dataRowPackets:           []*pg.Packet{pg.NewPacketWithType(pgtypes.ServerAuth)},
			responseWriter:           &fakeResponseWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:                      "it should call next middleware because dlp client is empty",
			client:                   nil,
			shouldCallNextMiddleware: true,
			shouldCallResponseWriter: false,
			dataRowPackets: []*pg.Packet{
				pg.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
			},
			responseWriter: &fakeResponseWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			redactmid, _ := NewRedactMiddleware(tt.client, "URL")
			if tt.maxPacketSize > 0 {
				redactmid.maxPacketLength = tt.maxPacketSize
			}
			for _, r := range tt.dataRowPackets {
				_, _ = tt.responseWriter.dataRowsBuf.Write(r.Encode())
				callNextMiddleware := false
				redactmid.Handler(func() { callNextMiddleware = true }, r, tt.responseWriter)
				if callNextMiddleware != tt.shouldCallNextMiddleware {
					t.Errorf("should call next middleware differs, got=%v, expected=%v", callNextMiddleware, tt.shouldCallNextMiddleware)
				}
			}
			if tt.responseWriter.executed != tt.shouldCallResponseWriter {
				t.Errorf("should call response writer differs, got=%v, expected=%v",
					tt.responseWriter.executed, tt.shouldCallResponseWriter)
			}
		})
	}

}

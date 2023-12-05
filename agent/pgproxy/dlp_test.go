package pgproxy

import (
	"bytes"
	"context"
	"testing"

	"github.com/runopsio/hoop/agent/dlp"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
)

type fakeClient struct {
	err error
}

func (c *fakeClient) DeidentifyContent(ctx context.Context, conf dlp.DeidentifyConfig, chunkIdx int, data dlp.InputData) *dlp.Chunk {
	dataRowEncoded := dlp.EncodeToDataRow(data.ContentItem().GetTable())
	chunk := dlp.NewChunk(dataRowEncoded).SetIndex(chunkIdx)
	if c.err != nil {
		chunk.SetError(c.err)
	}
	return chunk
}

func (c *fakeClient) ProjectID() string { return "" }

type fakeClientWriter struct {
	t           *testing.T
	dataRowsBuf *bytes.Buffer
	executed    bool
}

func (w *fakeClientWriter) Close() error { return nil }
func (w *fakeClientWriter) Write(got []byte) (int, error) {
	expected := w.dataRowsBuf.Bytes()
	if !bytes.Equal(expected, got) {
		w.t.Errorf("data row packet differs , got=%X, expected=%X", got, expected)
	}
	w.executed = true
	return 0, nil
}

func TestDlpHandler(t *testing.T) {
	for _, tt := range []struct {
		msg                  string
		client               dlp.Client
		clientWriter         *fakeClientWriter
		mustCallClientWriter bool
		err                  error
		infoTypes            []string
		maxPacketSize        int
		dataRowPackets       []*pgtypes.Packet
	}{
		{
			msg:    "it should buffer all data rows and redact it matching with the input",
			client: &fakeClient{},
			// shouldCallNextMiddleware: false,
			mustCallClientWriter: true,
			err:                  nil,
			infoTypes:            []string{"EMAIL_ADDRESS"},
			dataRowPackets: []*pgtypes.Packet{
				pgtypes.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
				pgtypes.NewDataRowPacket(3, "clara.shaw@sakilacustomer.org", "148", "2006-02-15 04:57:20"),
				pgtypes.NewDataRowPacket(3, "danny.isom@sakilacustomer.org", "404", "2006-02-15 04:57:20"),
				pgtypes.NewPacketWithType(pgtypes.ServerReadyForQuery),
			},
			clientWriter: &fakeClientWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:                  "it should not redact it because of the missing server ready packet",
			client:               &fakeClient{},
			mustCallClientWriter: false,
			err:                  nil,
			infoTypes:            []string{"EMAIL_ADDRESS"},
			dataRowPackets: []*pgtypes.Packet{
				pgtypes.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
				pgtypes.NewDataRowPacket(3, "clara.shaw@sakilacustomer.org", "148", "2006-02-15 04:57:20"),
				pgtypes.NewDataRowPacket(3, "danny.isom@sakilacustomer.org", "404", "2006-02-15 04:57:20"),
			},
			clientWriter: &fakeClientWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:    "it should redact it because it reached max packet size",
			client: &fakeClient{},
			// shouldCallNextMiddleware: false,
			mustCallClientWriter: true,
			maxPacketSize:        10,
			err:                  nil,
			infoTypes:            []string{"EMAIL_ADDRESS"},
			dataRowPackets: []*pgtypes.Packet{
				pgtypes.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
				pgtypes.NewDataRowPacket(3, "clara.shaw@sakilacustomer.org", "148", "2006-02-15 04:57:20"),
				pgtypes.NewDataRowPacket(3, "danny.isom@sakilacustomer.org", "404", "2006-02-15 04:57:20"),
			},
			clientWriter: &fakeClientWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:                  "it should be a noop error because it's not a data row packet",
			client:               &fakeClient{},
			err:                  errDLPNoop,
			mustCallClientWriter: false,
			infoTypes:            []string{"EMAIL_ADDRESS"},
			dataRowPackets:       []*pgtypes.Packet{pgtypes.NewPacketWithType(pgtypes.ServerAuth)},
			clientWriter:         &fakeClientWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:                  "it should be a noop error because dlp client is empty",
			client:               nil,
			err:                  errDLPNoop,
			mustCallClientWriter: false,
			infoTypes:            []string{"EMAIL_ADDRESS"},
			dataRowPackets: []*pgtypes.Packet{
				pgtypes.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
			},
			clientWriter: &fakeClientWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
		{
			msg:                  "it should be a noop error because there are no info types",
			client:               &fakeClient{},
			err:                  errDLPNoop,
			mustCallClientWriter: false,
			infoTypes:            nil,
			dataRowPackets: []*pgtypes.Packet{
				pgtypes.NewDataRowPacket(3, "andrea.henderson@sakilacustomer.org", "85", "2006-02-15 04:57:20"),
			},
			clientWriter: &fakeClientWriter{t: t, dataRowsBuf: bytes.NewBuffer([]byte{})},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			h := newDlpHandler(tt.client, tt.clientWriter, tt.infoTypes)
			if tt.maxPacketSize > 0 {
				h.maxPacketLength = tt.maxPacketSize
			}
			for _, pkt := range tt.dataRowPackets {
				_, _ = tt.clientWriter.dataRowsBuf.Write(pkt.Encode())
				if err := h.handle(pkt); err != tt.err {
					t.Fatalf("error does not match, got=%v, expected=%v", err, tt.err)
				}
			}
			if tt.clientWriter.executed != tt.mustCallClientWriter {
				t.Errorf("mustCallClientWriter attribute differs, got=%v, expected=%v",
					tt.clientWriter.executed, tt.mustCallClientWriter)
			}
		})
	}

}

package pg

import (
	"bytes"
	"testing"

	pgtypes "github.com/runopsio/hoop/common/pg"
)

func TestDecodeStartupPacketSSLRequest(t *testing.T) {
	sslRequestMsg := bytes.NewReader([]byte{0x00, 0x00, 0x00, 0x08, 0x04, 0x0d2, 0x16, 0x2f})
	gssEncRequestMsg := bytes.NewReader([]byte{0x00, 0x00, 0x00, 0x08, 0x04, 0x0d2, 0x16, 0x30})
	_, sslReqPkt, _ := DecodeStartupPacket(sslRequestMsg)
	_, gssReqPkt, _ := DecodeStartupPacket(gssEncRequestMsg)
	if !sslReqPkt.IsFrontendSSLRequest() {
		t.Error("expected ssl request")
	}
	if !gssReqPkt.IsFrontendSSLRequest() {
		t.Error("expected gss encryption request")
	}
}

func TestDecodeEncodeStartupPacket(t *testing.T) {
	startupPacket := []byte{
		0x00, 0x00, 0x00, 0x1f, // length
		0x00, 0x03, 0x00, 0x00, // major minor version
		0x75, 0x73, 0x65, 0x72, 0x00, // parameter name: user
		0x62, 0x6f, 0x62, 0x00, // parameter value: bob
		0x64, 0x61, 0x74, 0x61, 0x62, 0x61, 0x73, 0x65, 0x00, // parameter name: database
		0x62, 0x6f, 0x62, 0x00, // parameter value: bob
		0x00}
	_, pkt, err := DecodeStartupPacket(bytes.NewReader(startupPacket))
	if err != nil {
		t.Fatalf("don't expect error on decode, got=%v", err)
	}
	if pkt.typ != nil {
		t.Fatal("startup packet type must be nil")
	}
	if pkt.HeaderLength() != len(startupPacket) {
		t.Fatalf("wrong header sizer, want=%v, got=%v", len(startupPacket), pkt.HeaderLength())
	}
	if !bytes.Equal(pkt.frame, startupPacket[4:]) {
		t.Fatalf("malformed packet, want=% X, got=% X", startupPacket[4:], pkt.frame)
	}
	if !bytes.Equal(pkt.Encode(), startupPacket) {
		t.Fatalf("packet not re-encoded properly, want=%v, got=%v", pkt.Encode(), startupPacket)
	}
}

func TestDecodeStartupPacket(t *testing.T) {
	startupPacket := []byte{
		0x00, 0x00, 0x00, 0x1f, // length
		0x00, 0x03, 0x00, 0x00, // major minor version
		0x75, 0x73, 0x65, 0x72, 0x00, // parameter name: user
		0x62, 0x6f, 0x62, 0x00, // parameter value: bob
		0x64, 0x61, 0x74, 0x61, 0x62, 0x61, 0x73, 0x65, 0x00, // parameter name: database
		0x62, 0x6f, 0x62, 0x00, // parameter value: bob
		0x00}
	pgUsername := append([]byte(`mynewpguser`), '\000')
	pkt, err := DecodeStartupPacketWithUsername(bytes.NewReader(startupPacket), string(pgUsername))
	if err != nil {
		t.Fatalf("don't expect error on decode, got=%v", err)
	}
	if pkt.typ != nil {
		t.Fatal("startup packet type must be nil")
	}
	if !bytes.Contains(pkt.frame, pgUsername) {
		t.Fatal("expected username to be replaced by % X", pgUsername)
	}
}

func TestDecodeEncodeTypedPacket(t *testing.T) {
	authRequestTypedPacket := []byte{
		0x52, 0x00, 0x00, 0x00, 0x17, // type + header
		0x00, 0x00, 0x00, 0x0a, // auth length
		0x53, 0x43, 0x52, 0x41, 0x4d, 0x2d, 0x53, 0x48, 0x41, 0x2d, 0x32, 0x35, 0x36, 0x00, // scram-sha-256
		0x00,
	}
	_, pkt, err := DecodeTypedPacket(bytes.NewReader(authRequestTypedPacket))
	if err != nil {
		t.Fatalf("don't expect error on decode, got=%v", err)
	}
	if pkt.Type() != pgtypes.ServerAuth {
		t.Fatalf("decoded wrong type of packet, want=% X, got=% X", pgtypes.ServerAuth, pkt.Type())
	}
	if !bytes.Contains(pkt.Encode(), pkt.Encode()) {
		t.Fatalf("packet not re-encoded properly, want=%v, got=%v", pkt.Encode(), authRequestTypedPacket)
	}
}

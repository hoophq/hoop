package pgtypes

import (
	"bytes"
	"testing"
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

func TestDecodeEncodeStartupWithUsername(t *testing.T) {
	var (
		startupPacket = []byte{
			0x00, 0x00, 0x00, 0x67,
			0x00, 0x03, 0x00, 0x00,
			0x64, 0x61, 0x74, 0x65, 0x73, 0x74, 0x79, 0x6c, 0x65, 0x00, // datastyle
			0x49, 0x53, 0x4f, 0x2c, 0x20, 0x4d, 0x44, 0x59, 0x00, // ISO, MDY
			0x75, 0x73, 0x65, 0x72, 0x00, // user
			0x6e, 0x6f, 0x6f, 0x70, 0x7a, 0x69, 0x6b, 0x61, 0x00, // noopzika
			0x63, 0x6c, 0x69, 0x65, 0x6e, 0x74, 0x5f, 0x65, 0x6e, 0x63, 0x6f, 0x64, 0x69, 0x6e, 0x67, 0x00, // client_encoding
			0x55, 0x54, 0x46, 0x38, 0x00, // UTF8
			0x65, 0x78, 0x74, 0x72, 0x61, 0x5f, 0x66, 0x6c, 0x6f, 0x61, 0x74, 0x5f, 0x64, 0x69, 0x67, 0x69, 0x74, 0x73, 0x00, // extra_float_digits
			0x32, 0x00, // 2
			0x64, 0x61, 0x74, 0x61, 0x62, 0x61, 0x73, 0x65, 0x00, // database
			0x64, 0x65, 0x6c, 0x6c, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x00, // dellstore
			0x00}
		wantUser      = "newuser"
		newUserLength = len(wantUser)
	)

	pkt, err := DecodeStartupPacketWithUsername(bytes.NewReader(startupPacket), wantUser)
	if err != nil {
		t.Fatalf("don't expect error on decode, got=%v", err)
	}
	if pkt.typ != nil {
		t.Fatal("startup packet type must be nil")
	}
	gotUser := pkt.frame[9 : newUserLength+9]
	if string(gotUser) != wantUser {
		t.Errorf("fail to decode new users, got=%v, want=%v", string(gotUser), wantUser)
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
	if pkt.Type() != ServerAuth {
		t.Fatalf("decoded wrong type of packet, want=% X, got=% X", ServerAuth, pkt.Type())
	}
	if !bytes.Contains(pkt.Encode(), pkt.Encode()) {
		t.Fatalf("packet not re-encoded properly, want=%v, got=%v", pkt.Encode(), authRequestTypedPacket)
	}
}

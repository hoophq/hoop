package auth

import (
	"bytes"
	"encoding/hex"
	"io"
	"testing"

	"github.com/runopsio/hoop/agent/mysql"
	"github.com/runopsio/hoop/agent/mysql/types"
	"github.com/runopsio/hoop/common/log"
)

func decodeHex(v string) []byte { d, _ := hex.DecodeString(v); return d }

type fakeProxy struct {
	cli          io.WriteCloser
	srv          io.WriteCloser
	middlewareFn func(next mysql.NextFn, pkt *types.Packet, cli, srv io.WriteCloser)
}

func (p *fakeProxy) processPacket(source types.SourceType, pkt *types.Packet) (err error) {
	processNextMiddleware := false
	pktSink := types.NewPacket(pkt.Frame, pkt.Seq())
	pktSink.Source = source

	p.middlewareFn(func() { processNextMiddleware = true }, pktSink, p.cli, p.srv)
	if !processNextMiddleware {
		return nil
	}

	switch source {
	case types.SourceClient:
		_, err = p.srv.Write(pktSink.Encode())
	case types.SourceServer:
		_, err = p.cli.Write(pktSink.Encode())
	}
	return
}

type fakeWriter struct{ got []byte }

func (c *fakeWriter) Close() error { return nil }
func (c *fakeWriter) Write(p []byte) (int, error) {
	c.got = make([]byte, len(p))
	copy(c.got, p)
	return 0, nil
}

var (
	wantUser = "root"
	wantDB   = "sys"
)

// # To test this flow, run a mysql 5.7
//
// docker run --rm -p 3306:3306 --name mysql -e MYSQL_ROOT_PASSWORD=1a2b3c4d mysql:5.7
//
// # Connect it using the client arguments
//
// mysql -h 0 --port 3306 -D sys -u root -p1a2b3c4d -A --ssl-mode=DISABLED
//
// Then use wireshark to capute the packets of this flow
func TestMySQLNativeAuthOK(t *testing.T) {
	log.SetDefaultLoggerLevel("debug")
	hskBytes := decodeHex("0a352e372e3431004d000000281c01702e4a762600ffff080200ffc115000000000000000000006d22065a224f367b67464517006d7973716c5f6e61746976655f70617373776f726400")
	handshakePkt := types.NewPacket(hskBytes, 0)
	cli := &fakeWriter{got: []byte{}}
	srv := &fakeWriter{got: []byte{}}
	mproxy := New(wantUser, "")
	p := &fakeProxy{cli, srv, mproxy.Handler}
	// Initial Handshake Flow
	_ = p.processPacket(types.SourceServer, handshakePkt)

	if !bytes.Equal(cli.got, handshakePkt.Encode()) {
		t.Fatalf("server handshake packet must match.\ngot=% x\nwant=% x", cli.got, handshakePkt.Encode())
	}

	if mproxy.handshakePlugin != "mysql_native_password" {
		t.Fatalf("wrong plugin, want=mysql_native_password, got=% x", mproxy.handshakePlugin)
	}

	// Client Handshake Response
	clientHandshakeResponseBytes := decodeHex("8da6ff1900000001ff0000000000000000000000000000000000000000000000726f6f740014c5be7024fdba7e7159ea3e4585ec823b7f170c49737973006d7973716c5f6e61746976655f70617373776f72640072035f6f73076d61636f733132095f706c6174666f726d0561726d36340f5f636c69656e745f76657273696f6e06382e302e33300c5f636c69656e745f6e616d65086c69626d7973716c045f7069640437323930076f735f757365720373616e0c70726f6772616d5f6e616d65056d7973716c")
	clientHR := types.NewPacket(clientHandshakeResponseBytes, 1)
	_ = p.processPacket(types.SourceClient, clientHR)

	chr, err := parseClientHandshakeResponsePacket(wantUser, types.NewPacket(srv.got[4:], 1))
	if err != nil {
		t.Fatalf("fail parsing client handshake response, err=%v,\npacket=% x", err, clientHR.Encode())
	}
	if chr.authUser != wantUser || chr.databaseName != wantDB {
		t.Errorf("expected user=%v and database=%v to match. gotdb=%v, gotuser=%v\npacket=% x",
			wantUser, wantDB, chr.databaseName, chr.authUser, srv.got)
	}

	if chr.clientFlags.Has(types.ClientSSL) || chr.clientFlags.Has(types.ClientConnectAttrs) {
		t.Errorf("expect to disable ssl and connect attributes\ngotflags=%v\npacket=% x",
			chr.clientFlags.String(), clientHR.Encode())
	}

	// Auth Response Result
	okPacket := types.NewPacket(decodeHex("000000024000000006010403737973"), clientSequence)
	_ = p.processPacket(types.SourceServer, okPacket)
	if !mproxy.isAuthenticated || cli.got[3] != clientSequence {
		t.Errorf("expected to be in authenticated phase, gotseq=%x, wantseq=%x\npacket=%v",
			cli.got[3], clientSequence, cli.got)
	}
}

// # To test this flow, run a mysql 8.0.25
//
// docker run --rm -p 3306:3306 --name mysql -e MYSQL_ROOT_PASSWORD=1a2b3c4d mysql:8.0.30
//
// # Connect it using the client arguments
//
// mysql -h 0 --port 3306 -D sys -u root -p1a2b3c4d --ssl-mode=DISABLED --get-server-public-key
//
// Then use wireshark to capute the packets of this flow
func TestMySQLCachingSha2PasswordFullAuthOK(t *testing.T) {
	log.SetDefaultLoggerLevel("debug")
	hskBytes := decodeHex("0a382e302e3330000e0000002f524862231a313e00ffffff0200ffdf15000000000000000000006619566f58404a7f4a79375b0063616368696e675f736861325f70617373776f726400")
	handshakePkt := types.NewPacket(hskBytes, 0)
	cli := &fakeWriter{got: []byte{}}
	srv := &fakeWriter{got: []byte{}}
	mproxy := New(wantUser, "")
	p := &fakeProxy{cli, srv, mproxy.Handler}
	// Initial Handshake Flow
	_ = p.processPacket(types.SourceServer, handshakePkt)

	if !bytes.Equal(cli.got, handshakePkt.Encode()) {
		t.Fatalf("server handshake packet must match.\ngot=% x\nwant=% x", cli.got, handshakePkt.Encode())
	}

	if mproxy.handshakePlugin != "caching_sha2_password" {
		t.Fatalf("wrong plugin, want=caching_sha2_password, got=% x", mproxy.handshakePlugin)
	}

	// Client Handshake Response
	clientHandshakeResponseBytes := decodeHex("8da6ff1900000001ff0000000000000000000000000000000000000000000000726f6f740014c5be7024fdba7e7159ea3e4585ec823b7f170c49737973006d7973716c5f6e61746976655f70617373776f72640072035f6f73076d61636f733132095f706c6174666f726d0561726d36340f5f636c69656e745f76657273696f6e06382e302e33300c5f636c69656e745f6e616d65086c69626d7973716c045f7069640437323930076f735f757365720373616e0c70726f6772616d5f6e616d65056d7973716c")
	clientHR := types.NewPacket(clientHandshakeResponseBytes, 1)
	_ = p.processPacket(types.SourceClient, clientHR)

	chr, err := parseClientHandshakeResponsePacket(wantUser, types.NewPacket(srv.got[4:], 1))
	if err != nil {
		t.Fatalf("fail parsing client handshake response, err=%v,\npacket=% x", err, clientHR.Encode())
	}
	if chr.authUser != wantUser || chr.databaseName != wantDB {
		t.Errorf("expected user=%v and database=%v to match. gotdb=%v, gotuser=%v\npacket=% x",
			wantUser, wantDB, chr.databaseName, chr.authUser, srv.got)
	}

	if chr.clientFlags.Has(types.ClientSSL) || chr.clientFlags.Has(types.ClientConnectAttrs) {
		t.Errorf("expect to disable ssl and connect attributes\ngotflags=%v\npacket=% x",
			chr.clientFlags.String(), clientHR.Encode())
	}

	// Auth Switch Request Response
	switchReqPkt := types.NewPacket(decodeHex("0104"), 3)
	_ = p.processPacket(types.SourceServer, switchReqPkt)
	if mproxy.phase != phaseFullAuthPubKeyResponse {
		t.Fatalf("expected for pubkey response phase, got=%v\npacket=% x", mproxy.phase, srv.got)
	}

	// Auth Switch Response Result
	pubKeyPktBytes := decodeHex("012d2d2d2d2d424547494e205055424c4943204b45592d2d2d2d2d0a4d494942496a414e42676b71686b6947397730424151454641414f43415138414d49494243674b4341514541702f544f616e43574752456756624b382f324f520a7152304c4c7279572f30582f34654339563839774b55425268422f4f424d7a7377766b7155794a314939486d5045524e4c644e2f4652464334637a42673664410a44387173704f685736594b7064524a4e69704433416a744b584a5a556c5a687a4d56305573715738524c733930413857596a736c42624245794f477a704268720a584868506d6531677732795a6a2f6c4c65636b567a514b566a7a3579426636477a756c544d6a63396155686a3742543157386b4e4778546541646f6241524e350a5343784336366875464735774f6651706a6755596e79756257733178702f5948664e525165723854334735494e6b546b38494c553538463341347672526176710a355644393334476f6b52364c7772634d6c4131592f53672b53754150555a33515a3163334e2b675a59516c6a5048744c34546c746a317370616b6335454866310a54514944415141420a2d2d2d2d2d454e44205055424c4943204b45592d2d2d2d2d0a")
	pubKeyPkt := types.NewPacket(pubKeyPktBytes, 4)
	_ = p.processPacket(types.SourceServer, pubKeyPkt)
	if mproxy.phase != phaseReadAuthResult {
		t.Fatalf("expected for pubkey response phase, got=%v\npacket=% x", mproxy.phase, srv.got)
	}

	// Auth Response Result
	okPkt := types.NewPacket(decodeHex("000000024000000006010403737973"), 4)
	_ = p.processPacket(types.SourceServer, okPkt)
	if !mproxy.isAuthenticated || cli.got[3] != clientSequence {
		t.Errorf("expected to be in authenticated phase, gotseq=%x, wantseq=%x\npacket=%v",
			cli.got[3], clientSequence, cli.got)
	}
}

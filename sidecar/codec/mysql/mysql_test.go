package mysql_test

import (
	"encoding/binary"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/codec/mysql"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func pkt(seq byte, payload []byte) []byte {
	n := len(payload)
	return append([]byte{byte(n), byte(n >> 8), byte(n >> 16), seq}, payload...)
}

func lenenc(dst []byte, s string) []byte { return append(append(dst, byte(len(s))), s...) }

// handshake is a HandshakeResponse41 with PROTOCOL_41 and CONNECT_ATTRS and
// NUL-terminated user and auth, the smallest shape that carries attributes.
func handshake(attrs ...string) []byte {
	p := binary.LittleEndian.AppendUint32(nil, 1<<9|1<<20)
	p = append(p, make([]byte, 28)...)
	p = append(p, "app\x00secret\x00"...)
	var pairs []byte
	for _, s := range attrs {
		pairs = lenenc(pairs, s)
	}
	return pkt(1, lenenc(p, string(pairs)))
}

func decode(t *testing.T, c inspect.Codec, dir inspect.Direction, b []byte) []inspect.Statement {
	t.Helper()
	stmts, n, err := c.Decode(dir, b)
	if err != nil || n != len(b) {
		t.Fatalf("decode %v: n=%d of %d, err=%v", dir, n, len(b), err)
	}
	return stmts
}

// The seam must keep the review-mode attribute, or a MySQL client's opt-in
// never reaches the analyzer and the listener's mode applies silently.
func TestNewKeepsTheReviewModeAttribute(t *testing.T) {
	c := mysql.New()
	decode(t, c, inspect.FromServer, pkt(0, append([]byte{10}, make([]byte, 40)...)))
	decode(t, c, inspect.FromClient, handshake("_client_name", "libmysql", analyzer.ConnectAttrReviewMode, "return"))
	decode(t, c, inspect.FromServer, pkt(2, []byte{0, 0, 0, 2, 0, 0, 0}))
	stmts := decode(t, c, inspect.FromClient, pkt(0, []byte("\x03DELETE FROM users")))

	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	key := inspect.MetadataMySQLConnectAttrPrefix + analyzer.ConnectAttrReviewMode
	if got := stmts[0].Metadata[key]; got != "return" {
		t.Errorf("%s = %q, want return", key, got)
	}
	if _, kept := stmts[0].Metadata[inspect.MetadataMySQLConnectAttrPrefix+"_client_name"]; kept {
		t.Error("kept _client_name, which nothing asked for")
	}
}

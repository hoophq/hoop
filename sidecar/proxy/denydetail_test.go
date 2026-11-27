package proxy_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/codec/example"
	httpcodec "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
	codecmongodb "github.com/hoophq/libhoop/v2/codec/mongodb"
	codecoracle "github.com/hoophq/libhoop/v2/codec/oracle"
)

const (
	denyTitle  = "unbounded delete"
	denyDetail = "The DELETE has no WHERE clause, so it removes every row."
)

var oracleMeta = map[string]string{
	"oracle.ttc_version": "27", "oracle.oci": "true", "oracle.oci_word": "8",
	"oracle.sb1_compressed": "false", "oracle.end_of_response": "false",
	"oracle.big_clr": "false", "oracle.big_length": "1",
}

func denyStatement(proto inspect.Protocol) inspect.Statement {
	st := inspect.Statement{Protocol: proto, Direction: inspect.FromClient}
	switch proto {
	case inspect.MongoDB:
		st.Metadata = map[string]string{codecmongodb.MetadataRequestID: "7"}
	case inspect.Oracle:
		st.Metadata = oracleMeta
	}
	return st
}

// pgErrorFields reads the fields of one ErrorResponse.
func pgErrorFields(t *testing.T, frame []byte) map[byte]string {
	t.Helper()
	if len(frame) < 6 || frame[0] != 'E' {
		t.Fatalf("not an ErrorResponse: %q", frame)
	}
	if n := binary.BigEndian.Uint32(frame[1:5]); int(n) != len(frame)-1 {
		t.Fatalf("declared length %d, frame %d", n, len(frame)-1)
	}
	fields := map[byte]string{}
	for body := frame[5:]; len(body) > 1; {
		end := bytes.IndexByte(body[1:], 0)
		if end < 0 {
			t.Fatalf("unterminated field in %q", frame)
		}
		fields[body[0]] = string(body[1 : 1+end])
		body = body[2+end:]
	}
	return fields
}

// mssqlErrorText decodes the message of the ERROR token MSSQLError writes.
func mssqlErrorText(t *testing.T, frame []byte) string {
	t.Helper()
	const msgAt = 8 + 3 + 4 + 1 + 1 + 2
	if len(frame) < msgAt || frame[8] != 0xAA {
		t.Fatalf("not a TDS ERROR token: %x", frame)
	}
	chars := int(binary.LittleEndian.Uint16(frame[msgAt-2 : msgAt]))
	units := make([]uint16, chars)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(frame[msgAt+2*i:])
	}
	return string(utf16.Decode(units))
}

func TestEveryDenyFrameCarriesTheDetail(t *testing.T) {
	const joined = denyTitle + ": " + denyDetail
	w := proxy.ProtocolDenyWriter{}
	for _, tc := range []struct {
		proto inspect.Protocol
		check func(t *testing.T, frame []byte)
	}{
		{inspect.Postgres, func(t *testing.T, frame []byte) {
			f := pgErrorFields(t, frame)
			if f['M'] != denyTitle || f['D'] != denyDetail {
				t.Errorf("message %q, detail %q", f['M'], f['D'])
			}
		}},
		{inspect.MySQL, func(t *testing.T, frame []byte) {
			if got := string(frame[13:]); got != joined {
				t.Errorf("message = %q", got)
			}
		}},
		{inspect.MSSQL, func(t *testing.T, frame []byte) {
			if got := mssqlErrorText(t, frame); got != joined {
				t.Errorf("message = %q", got)
			}
		}},
		{inspect.HTTP, func(t *testing.T, frame []byte) {
			if !strings.HasSuffix(string(frame), "\r\n\r\n"+joined+"\n") {
				t.Errorf("body = %q", frame)
			}
		}},
		{inspect.ClickHouse, func(t *testing.T, frame []byte) {
			if !bytes.Contains(frame, []byte(joined)) {
				t.Errorf("frame = %q", frame)
			}
		}},
		{inspect.MongoDB, func(t *testing.T, frame []byte) {
			if !bytes.Contains(frame, []byte(joined)) {
				t.Errorf("frame = %q", frame)
			}
		}},
		{inspect.Oracle, func(t *testing.T, frame []byte) {
			if !bytes.Contains(frame, []byte("ORA-01031: "+joined)) {
				t.Errorf("frame = %q", frame)
			}
		}},
	} {
		t.Run(string(tc.proto), func(t *testing.T) {
			frame := w.DenyStatement(denyStatement(tc.proto), denyTitle, denyDetail, nil)
			if len(frame) == 0 {
				t.Fatal("no frame")
			}
			tc.check(t, frame)
		})
	}
}

// A lane with no analyzer sends no detail, and its frames stay the bytes
// they were.
func TestAnEmptyDetailKeepsEveryFrame(t *testing.T) {
	w := proxy.ProtocolDenyWriter{}
	for _, proto := range []inspect.Protocol{
		inspect.Postgres, inspect.MySQL, inspect.MSSQL, inspect.HTTP, inspect.ClickHouse,
	} {
		if !bytes.Equal(w.DenyStatement(denyStatement(proto), "x", "", nil), w.Deny(proto, inspect.FromClient, "x")) {
			t.Errorf("%s: the frame changed", proto)
		}
	}
	if !bytes.Equal(w.DenyStatement(denyStatement(inspect.MongoDB), "x", "", nil), codecmongodb.DenyResponse(7, "x")) {
		t.Error("mongodb: the frame changed")
	}
	if !bytes.Equal(w.DenyStatement(denyStatement(inspect.Oracle), "x", "", nil), codecoracle.DenyResponse(oracleMeta, "x")) {
		t.Error("oracle: the frame changed")
	}
}

// An Oracle error holds 254 bytes. A long detail is cut to fit rather than
// costing the client its frame.
func TestAnOracleFrameCutsALongDetail(t *testing.T) {
	frame := proxy.ProtocolDenyWriter{}.DenyStatement(denyStatement(inspect.Oracle), denyTitle,
		strings.Repeat("é", 300), nil)
	if frame == nil {
		t.Fatal("no frame")
	}
	if !bytes.Contains(frame, []byte("ORA-01031: "+denyTitle+": é")) || !bytes.Contains(frame, []byte("...\n")) {
		t.Errorf("frame = %q", frame)
	}
}

func TestAPostgresFrameDropsNULBytes(t *testing.T) {
	f := pgErrorFields(t, proxy.PostgresErrorDetail("a\x00b", "c\x00d"))
	if f['M'] != "ab" || f['D'] != "cd" {
		t.Errorf("message %q, detail %q", f['M'], f['D'])
	}
}

type explainingModel struct{}

func (explainingModel) Name() string { return "stub" }

func (explainingModel) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: analyzer.RiskHigh, Title: denyTitle, Explanation: denyDetail}, nil
}

func assertNotInTrail(t *testing.T, sink *audit.MemorySink) {
	t.Helper()
	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("nothing was recorded")
	}
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "WHERE clause") {
			t.Errorf("the audit trail carries the explanation: %s", b)
		}
	}
}

// The analyzer's explanation reaches a psql client as DETAIL, and the
// statement's audit record keeps the title only.
func TestAnAnalyzerBlockSendsItsExplanationToThePostgresClientOnly(t *testing.T) {
	ev, err := analyzer.New(analyzer.Config{
		Rule:     "appdb",
		Provider: explainingModel{},
		Trigger:  analyzer.Trigger{All: true},
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
	})
	if err != nil {
		t.Fatalf("analyzer.New: %v", err)
	}
	up := newEchoUpstream(t, nil)
	sink := audit.NewMemorySink(64)
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     ev,
		Audit:      sink,
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write(pgQuery("DELETE FROM customers")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	frame, _ := io.ReadAll(c)
	f := pgErrorFields(t, frame)
	if f['M'] != denyTitle || f['D'] != denyDetail {
		t.Errorf("message %q, detail %q", f['M'], f['D'])
	}
	if len(up.got()) != 0 {
		t.Error("the blocked statement reached the upstream")
	}
	var violation bool
	for _, e := range sink.Events() {
		if e.Kind == audit.KindViolation {
			violation = e.Message == denyTitle
		}
	}
	if !violation {
		t.Errorf("no violation recorded with the title: %+v", sink.Events())
	}
	assertNotInTrail(t, sink)
}

type detailPolicy struct{}

func (detailPolicy) Evaluate(inspect.Statement) policy.Verdict {
	v := policy.Deny("appdb", denyTitle)
	v.Detail = denyDetail
	return v
}

// A plug-in codec writes its own frame, which gets the one-line text.
func TestADenialDetailReachesACodecDenyFrame(t *testing.T) {
	up := newEchoUpstream(t, nil)
	srv := startServer(t, proxy.Config{
		Upstream:     up.addr(),
		Protocol:     example.Protocol,
		Policy:       detailPolicy{},
		DenyWriter:   proxy.ProtocolDenyWriter{},
		CodecFactory: example.New,
	})
	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write(append([]byte{5}, "hello"...)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, _ := io.ReadAll(c)
	joined := denyTitle + ": " + denyDetail
	if want := append([]byte{0xFF, byte(len(joined))}, joined...); !bytes.Equal(got, want) {
		t.Errorf("deny frame = %q, want %q", got, want)
	}
}

func TestADenialDetailReachesTheHTTPClientOnly(t *testing.T) {
	up := newRouteUpstream(t, nil)
	sink := audit.NewMemorySink(64)
	srv := startServer(t, proxy.Config{
		Upstream:     up.addr(),
		Protocol:     inspect.HTTP,
		Policy:       detailPolicy{},
		Audit:        sink,
		DenyWriter:   proxy.ProtocolDenyWriter{},
		CodecFactory: func() inspect.Codec { return httpcodec.New(httpcodec.Options{}) },
	})
	got := roundTrip(t, srv.Addr().String(), "DELETE /orders HTTP/1.1\r\nHost: h\r\n\r\n")
	if want := "403 Forbidden\n" + denyTitle + ": " + denyDetail + "\n"; got != want {
		t.Errorf("response = %q, want %q", got, want)
	}
	assertNotInTrail(t, sink)
}

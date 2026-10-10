package wasm

// The x-acmewire module is the Rust SDK's example plug-in, built from
// sdk/rust/examples/acmewire. It carries every capability, so it is what
// proves the rewrite, credential, content and filter paths end to end,
// through the gate where the gate is the consumer.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// aw frames one acmewire message: opcode, big-endian length, payload.
func aw(op byte, payload string) []byte {
	f := make([]byte, 5+len(payload))
	f[0] = op
	binary.BigEndian.PutUint32(f[1:5], uint32(len(payload)))
	copy(f[5:], payload)
	return f
}

// awRow frames a 'D' message: each cell a big-endian length and bytes.
func awRow(cells ...string) []byte {
	var payload []byte
	for _, c := range cells {
		payload = binary.BigEndian.AppendUint32(payload, uint32(len(c)))
		payload = append(payload, c...)
	}
	return aw('D', string(payload))
}

func acmewire(t *testing.T) *Plugin {
	t.Helper()
	return load(t, "acmewire.wasm")
}

func TestAcmewireManifest(t *testing.T) {
	p := acmewire(t)
	m := p.ManifestValues()
	if m.Protocol != "x-acmewire" || m.Label != "Acme Wire" || m.SQLDialect != "mysql" {
		t.Fatalf("manifest %+v", m)
	}
	for _, c := range []string{CapDeny, CapFilter, CapRewrite, CapCredential, CapContent} {
		if !m.hasCapability(c) {
			t.Fatalf("capability %s missing from %v", c, m.Capabilities)
		}
	}
	factory := func() inspect.Codec { return p.NewCodec(nil) }
	if !gate.MaskSupportedBy(factory) {
		t.Fatal("rewrite capability declared but MaskSupportedBy says no")
	}
	c := factory()
	if _, ok := c.(gate.Reframer); !ok {
		t.Fatal("rewrite capability declared but the codec is no gate.Reframer")
	}
	if _, ok := c.(analyzer.ContentRenderer); !ok {
		t.Fatal("content capability declared but the codec is no analyzer.ContentRenderer")
	}
}

func TestAcmewireQueryUsesTheHostLexer(t *testing.T) {
	p := acmewire(t)
	c := p.NewCodec(nil)
	stmts, n, err := c.Decode(inspect.FromClient, aw('Q', "DELETE FROM `Orders` WHERE id = 7"))
	if err != nil || n != 5+len("DELETE FROM `Orders` WHERE id = 7") {
		t.Fatalf("decode: %v %d", err, n)
	}
	s := stmts[0]
	if s.Protocol != "x-acmewire" || s.Operation != inspect.OpDelete || len(s.Tables) != 1 || s.Tables[0] != "orders" {
		t.Fatalf("statement %+v", s)
	}
	if s.Metadata["x-acmewire.verb"] != "QUERY" {
		t.Fatalf("metadata %v", s.Metadata)
	}
}

// cellMasker masks the cells of one column and reports the entity.
type cellMasker struct{ column string }

func (cellMasker) Mask(data []byte) ([]byte, []string, int) { return data, nil, 0 }
func (m cellMasker) MaskCell(column string, value []byte) ([]byte, []string, int) {
	if column != m.column {
		return value, nil, 0
	}
	return []byte("***"), []string{"EMAIL"}, 1
}

func TestAcmewireRewriteThroughTheGate(t *testing.T) {
	p := acmewire(t)
	ctx := context.Background()
	g, err := gate.New(session.New("x-acmewire", session.Identity{Subject: "t"}), gate.Config{
		Protocol:     "x-acmewire",
		CodecFactory: func() inspect.Codec { return p.NewCodec(nil) },
		Masker:       cellMasker{column: "email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close(ctx)

	d := g.Response(ctx, aw('C', "id\x00email"))
	if !d.Allowed || d.Err != nil || len(d.Statements) != 1 || d.Statements[0].Result == nil || len(d.Statements[0].Result.Columns) != 2 {
		t.Fatalf("columns decision %+v err %v", d, d.Err)
	}
	d = g.Response(ctx, awRow("1", "alice@example.com"))
	if !d.Allowed || d.Err != nil {
		t.Fatalf("row decision %+v err %v", d, d.Err)
	}
	if want := awRow("1", "***"); string(d.Payload) != string(want) {
		t.Fatalf("payload %q, want %q", d.Payload, want)
	}
	if d.MaskedCount != 1 || len(d.Masked) != 1 || d.Masked[0] != "EMAIL" {
		t.Fatalf("masked %v count %d", d.Masked, d.MaskedCount)
	}
	if d.Statements[0].Result == nil || d.Statements[0].Result.RowCount != 1 {
		t.Fatalf("row statement %+v", d.Statements[0])
	}
	// A row split across two chunks: the guest holds the head until the
	// tail arrives, then rebuilds the whole frame.
	row := awRow("2", "bob@example.com")
	head, tail := row[:8], row[8:]
	if d = g.Response(ctx, head); !d.Allowed || len(d.Payload) != 0 {
		t.Fatalf("head decision %+v", d)
	}
	if d = g.Response(ctx, tail); !d.Allowed || string(d.Payload) != string(awRow("2", "***")) {
		t.Fatalf("tail decision payload %q", d.Payload)
	}
	// The done frame carries no cells and passes through unchanged.
	if d = g.Response(ctx, aw('R', "\x00\x00\x00\x02")); !d.Allowed || string(d.Payload) != string(aw('R', "\x00\x00\x00\x02")) || d.Statements[0].Result.RowCount != 2 {
		t.Fatalf("done decision %+v", d)
	}
}

// noTokenText fails when any field of s, in its JSON form, carries the
// credential text: the statement is what audit, policy and the analyzer
// see, so the module must leave only a handle on it.
func noTokenText(t *testing.T, s inspect.Statement, token string) {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatalf("credential text on the statement: %s", raw)
	}
}

func TestAcmewireCredential(t *testing.T) {
	p := acmewire(t)
	c := p.NewCodec(nil)
	stmts, _, err := c.Decode(inspect.FromClient, aw('A', "s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	s := stmts[0]
	if s.Text != "AUTH" || s.Metadata["x-acmewire.credential"] != "1" {
		t.Fatalf("auth statement %+v", s)
	}
	noTokenText(t, s, "s3cret")
	cred, ok := c.(gate.CredentialSource).TakeCredential(&s)
	if !ok || cred != "s3cret" {
		t.Fatalf("TakeCredential: %q %v", cred, ok)
	}
	if _, present := s.Metadata["x-acmewire.credential"]; present || s.Protocol != "x-acmewire" || s.Direction != inspect.FromClient || s.Text != "AUTH" {
		t.Fatalf("statement after lift %+v", s)
	}
	// The handle is spent: a second lift of the same statement, with the
	// handle put back, yields nothing and leaves the statement alone.
	again := stmts[0]
	if cred, ok := c.(gate.CredentialSource).TakeCredential(&again); ok || cred != "" {
		t.Fatalf("second lift returned %q %v", cred, ok)
	}
	if again.Metadata["x-acmewire.credential"] != "1" || again.Text != "AUTH" {
		t.Fatalf("statement after the refused lift %+v", again)
	}
	// A statement without a credential lifts nothing.
	q, _, _ := c.Decode(inspect.FromClient, aw('Q', "SELECT 1"))
	if cred, ok := c.(gate.CredentialSource).TakeCredential(&q[0]); ok || cred != "" {
		t.Fatalf("query lifted %q", cred)
	}
}

// recordingIdentity resolves every credential it sees to a subject
// derived from it, so a test can see what reached it.
type recordingIdentity struct{ seen []string }

func (r *recordingIdentity) Resolve(_ context.Context, credential string) (session.Identity, error) {
	r.seen = append(r.seen, credential)
	return session.Identity{Subject: "user-of-" + credential}, nil
}

func TestAcmewireCredentialThroughTheGate(t *testing.T) {
	p := acmewire(t)
	ctx := context.Background()
	resolver := &recordingIdentity{}
	g, err := gate.New(session.New("x-acmewire", session.Identity{Subject: "anonymous"}), gate.Config{
		Protocol:        "x-acmewire",
		CodecFactory:    func() inspect.Codec { return p.NewCodec(nil) },
		RequestIdentity: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close(ctx)
	d := g.Request(ctx, aw('A', "s3cret"))
	if !d.Allowed || d.Err != nil {
		t.Fatalf("auth decision %+v err %v", d, d.Err)
	}
	if len(resolver.seen) != 1 || resolver.seen[0] != "s3cret" {
		t.Fatalf("resolver saw %v", resolver.seen)
	}
	if got := g.Session().Identity.Subject; got != "user-of-s3cret" {
		t.Fatalf("session identity %q", got)
	}
	// The token never reaches the statement the gate records, and the
	// spent handle leaves with it.
	for _, s := range d.Statements {
		noTokenText(t, s, "s3cret")
		if _, present := s.Metadata["x-acmewire.credential"]; present {
			t.Fatalf("handle survived the lift: %+v", s)
		}
	}
}

func TestAcmewireContent(t *testing.T) {
	p := acmewire(t)
	c := p.NewCodec(nil)
	stmts, _, err := c.Decode(inspect.FromClient, aw('P', "Orders2024"))
	if err != nil {
		t.Fatal(err)
	}
	s := stmts[0]
	if s.Operation != inspect.OpDelete || s.Metadata["x-acmewire.verb"] != "PURGE" {
		t.Fatalf("purge statement %+v", s)
	}
	renderer := c.(analyzer.ContentRenderer)
	text, key, ok := renderer.Content(s, 1<<20)
	if !ok {
		t.Fatal("content: ok=false for a purge")
	}
	if want := "Protocol: x-acmewire\nVerb: PURGE\n\n" + s.Text; text != want {
		t.Fatalf("content text %q, want %q", text, want)
	}
	digitless := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, strings.ToLower(s.Text))
	if key != "PURGE|"+digitless {
		t.Fatalf("cache key %q", key)
	}
	// The host bounds the text: the guest never sees maxBytes.
	if text, _, ok = renderer.Content(s, 12); !ok || !strings.HasSuffix(text, "[truncated]") || !strings.HasPrefix(text, "Protocol: x-") {
		t.Fatalf("truncated content %q %v", text, ok)
	}
	// Server statements carry nothing to classify.
	srv, _, _ := c.Decode(inspect.FromServer, aw('R', "\x00\x00\x00\x01"))
	if _, _, ok := renderer.Content(srv[0], 1<<20); ok {
		t.Fatal("content: ok=true for a server statement")
	}
}

func TestAcmewireFilterStripsKeepalives(t *testing.T) {
	p := acmewire(t)
	c := p.NewCodec(nil)
	padded := append([]byte{0, 0, 0}, aw('Q', "SELECT 1")...)
	out, err := c.(gate.StreamFilter).Filter(inspect.FromClient, padded)
	if err != nil || string(out) != string(aw('Q', "SELECT 1")) {
		t.Fatalf("filter: %v %q", err, out)
	}
	// Through the gate: the padding never reaches decode.
	ctx := context.Background()
	g, err := gate.New(session.New("x-acmewire", session.Identity{Subject: "t"}), gate.Config{
		Protocol:     "x-acmewire",
		CodecFactory: func() inspect.Codec { return p.NewCodec(nil) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close(ctx)
	d := g.Request(ctx, padded)
	if !d.Allowed || d.Err != nil || len(d.Statements) != 1 || d.Statements[0].Text != "SELECT 1" {
		t.Fatalf("decision %+v err %v", d, d.Err)
	}
	if string(d.Payload) != string(aw('Q', "SELECT 1")) {
		t.Fatalf("forwarded %q with the padding", d.Payload)
	}
}

func TestAcmewireDenyPrefixOption(t *testing.T) {
	p := acmewire(t)
	c := p.NewCodec(nil)
	if f := c.(gate.DenyFramer).DenyFrame(inspect.FromClient, "nope"); string(f) != string(aw('E', "ACME: nope")) {
		t.Fatalf("default prefix: %q", f)
	}
	c = p.NewCodec(map[string]string{"deny_prefix": "WIRE"})
	if f := c.(gate.DenyFramer).DenyFrame(inspect.FromClient, "nope"); string(f) != string(aw('E', "WIRE: nope")) {
		t.Fatalf("configured prefix: %q", f)
	}
	// The guest refuses an empty prefix at open; the codec reports it.
	c = p.NewCodec(map[string]string{"deny_prefix": ""})
	if _, _, err := c.Decode(inspect.FromClient, aw('Q', "SELECT 1")); err == nil || !strings.Contains(err.Error(), "open refused the connection") {
		t.Fatalf("empty prefix: %v", err)
	}
	// A decode error on the gate path denies with the guest's own frame.
	ctx := context.Background()
	g, err := gate.New(session.New("x-acmewire", session.Identity{Subject: "t"}), gate.Config{
		Protocol:     "x-acmewire",
		CodecFactory: func() inspect.Codec { return p.NewCodec(nil) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close(ctx)
	d := g.Request(ctx, aw('X', ""))
	if d.Allowed || d.Rule != "stream-unsafe" {
		t.Fatalf("unknown opcode decision %+v", d)
	}
	if f, ok := g.DenyFrame(inspect.FromClient, d.Message); !ok || !strings.HasPrefix(string(f), "E") || !strings.Contains(string(f), "ACME: ") {
		t.Fatalf("deny frame after a decode error %q %v", f, ok)
	}
}

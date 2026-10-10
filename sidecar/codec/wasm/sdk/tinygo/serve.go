package codec

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
)

// Codec is one connection's decoder. The host creates one through the
// factory given to Serve, calls Open with the listener's options, and
// drops it at close.
//
// The optional capabilities are the interfaces below. The export for each
// exists only when the build turns on its tag, so the host never reaches
// an unimplemented one; describe traps when a declared capability's
// interface is missing on the codec.
type Codec interface {
	// Open begins a connection. options is the listener's settings, keyed
	// by the manifest's option names. An error refuses the connection.
	Open(options map[string]string) error

	// Decode turns data, which begins at a message boundary, into
	// statements. Decoded.Consumed stops at the first byte of an
	// incomplete trailing message. An error means the bytes are malformed
	// for this protocol and the host drops the connection.
	Decode(dir Direction, data []byte) (Decoded, error)
}

// Denier renders a message as the protocol's native error frame, which
// the host sends before closing a denied connection. Tag hoop_deny.
type Denier interface {
	Deny(dir Direction, message string) []byte
}

// Filterer transforms bytes before inspection and forwarding. It may hold
// a prefix and return nothing. Tag hoop_filter.
type Filterer interface {
	Filter(dir Direction, data []byte) []byte
}

// Rewriter masks responses by rebuilding their frames. Tag hoop_rewrite.
type Rewriter interface {
	// EnableRewrite runs before any server bytes arrive, only when the
	// lane has a masker.
	EnableRewrite()
	// Rewrite receives every server chunk after Decode saw it, hands each
	// cell it can name to mask, and returns the rebuilt frames. The codec
	// may hold rows until it can rebuild them.
	Rewrite(data []byte, mask MaskFunc) (Rewritten, error)
	// Flush releases every row Rewrite held; called when the connection
	// ends.
	Flush(mask MaskFunc) (Rewritten, error)
}

// CredentialSource lifts the credential out of a request statement. Tag
// hoop_credential.
type CredentialSource interface {
	// TakeCredential returns the credential and the statement scrubbed of
	// every trace of it; ok is false when the request carries none.
	TakeCredential(stmt Statement) (credential string, scrubbed Statement, ok bool)
}

// ContentRenderer renders a statement for the AI analyzer. Tag
// hoop_content.
type ContentRenderer interface {
	// Content returns false when there is nothing to classify. The host
	// bounds the text to the analyzer's budget.
	Content(stmt Statement) (Content, bool)
}

// registry is the codecs of one instance keyed by the conn the host
// passes: one entry under per_connection instancing, one per open
// connection under per_lane. The ABI serializes calls into an instance,
// so a plain map is enough.
type registry struct {
	factory  func() Codec
	manifest Manifest
	conns    map[uint32]Codec
}

// declared is the capability set the build tags turned on; each tagged
// export file appends its own in an init function.
var declared []Capability

var served *registry

// Serve installs the codec the exports dispatch to. Call it from an init
// function: under TinyGo's wasm-unknown target main never runs, and the
// host calls describe before anything else.
func Serve(factory func() Codec, manifest Manifest) {
	served = newRegistry(factory, manifest)
}

func newRegistry(factory func() Codec, manifest Manifest) *registry {
	manifest.ABI = ABIVersion
	return &registry{factory: factory, manifest: manifest, conns: map[uint32]Codec{}}
}

func current() *registry {
	if served == nil {
		panic("hoop-codec: codec.Serve was not called; call it from an init function")
	}
	return served
}

// describe renders the manifest, holding its capability list to the
// exports the build tags emitted and the interfaces the codec implements.
// A mismatch traps, which refuses the module at load and names the cause.
func (r *registry) describe() []byte {
	want := append([]Capability(nil), r.manifest.Capabilities...)
	have := append([]Capability(nil), declared...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	sort.Slice(have, func(i, j int) bool { return have[i] < have[j] })
	if fmt.Sprint(want) != fmt.Sprint(have) {
		panic(fmt.Sprintf("hoop-codec: manifest capabilities %v but the build tags export %v; the two must name the same set", want, have))
	}
	probe := r.factory()
	for _, c := range have {
		var ok bool
		switch c {
		case CapDeny:
			_, ok = probe.(Denier)
		case CapFilter:
			_, ok = probe.(Filterer)
		case CapRewrite:
			_, ok = probe.(Rewriter)
		case CapCredential:
			_, ok = probe.(CredentialSource)
		case CapContent:
			_, ok = probe.(ContentRenderer)
		}
		if !ok {
			panic(fmt.Sprintf("hoop-codec: capability %s is declared but %T does not implement its interface", c, probe))
		}
	}
	out, err := json.Marshal(r.manifest)
	if err != nil {
		panic("hoop-codec: manifest does not serialize: " + err.Error())
	}
	return out
}

// open returns 0, or 1 for an options object the SDK cannot read, or 2
// for a codec that refused.
func (r *registry) open(conn uint32, optionsJSON []byte) uint32 {
	options := map[string]string{}
	if len(optionsJSON) > 0 {
		if err := json.Unmarshal(optionsJSON, &options); err != nil {
			Log(LevelError, fmt.Sprintf("hoop-codec: options for connection %d are not a string map: %v", conn, err))
			return 1
		}
	}
	c := r.factory()
	if err := c.Open(options); err != nil {
		Log(LevelError, fmt.Sprintf("hoop-codec: connection %d refused: %v", conn, err))
		return 2
	}
	r.conns[conn] = c
	return 0
}

func (r *registry) close(conn uint32) {
	delete(r.conns, conn)
}

func (r *registry) decode(conn, dir uint32, data []byte) []byte {
	d := directionOf(dir)
	c, ok := r.conns[conn]
	if !ok {
		// A decode on a connection the host never opened is a host bug.
		// Reporting it as a decode error drops that connection alone,
		// the proportionate failure under per_lane.
		return errorJSON(fmt.Sprintf("hoop-codec: connection %d was not opened", conn))
	}
	decoded, err := c.Decode(d, data)
	if err != nil {
		return errorJSON(err.Error())
	}
	if decoded.Statements == nil {
		decoded.Statements = []Statement{}
	}
	return mustJSON(decodeResult{Statements: decoded.Statements, Consumed: decoded.Consumed})
}

// get returns the codec of conn for a capability export. Those have no
// error channel, so a connection the host never opened traps: the host
// reports the failure and fails that stream closed.
func (r *registry) get(conn uint32, export string) Codec {
	c, ok := r.conns[conn]
	if !ok {
		panic(fmt.Sprintf("hoop-codec: %s on connection %d, which was not opened", export, conn))
	}
	return c
}

func (r *registry) deny(conn, dir uint32, message []byte) []byte {
	return r.get(conn, "deny").(Denier).Deny(directionOf(dir), string(message))
}

func (r *registry) filter(conn, dir uint32, data []byte) []byte {
	return r.get(conn, "filter").(Filterer).Filter(directionOf(dir), data)
}

func (r *registry) enableRewrite(conn uint32) {
	r.get(conn, "enable_rewrite").(Rewriter).EnableRewrite()
}

func (r *registry) rewrite(conn uint32, data []byte) []byte {
	return rewriteJSON(r.get(conn, "rewrite").(Rewriter).Rewrite(data, mask))
}

func (r *registry) flush(conn uint32) []byte {
	return rewriteJSON(r.get(conn, "flush").(Rewriter).Flush(mask))
}

func (r *registry) takeCredential(conn uint32, stmtJSON []byte) []byte {
	credential, scrubbed, ok := r.get(conn, "take_credential").(CredentialSource).TakeCredential(parseStatement(stmtJSON))
	if !ok {
		return mustJSON(credentialResult{OK: false})
	}
	return mustJSON(credentialResult{OK: true, Credential: credential, Statement: &scrubbed})
}

func (r *registry) content(conn uint32, stmtJSON []byte) []byte {
	content, ok := r.get(conn, "content").(ContentRenderer).Content(parseStatement(stmtJSON))
	if !ok {
		return mustJSON(contentResult{OK: false})
	}
	return mustJSON(contentResult{OK: true, Text: content.Text, CacheKey: content.CacheKey})
}

func parseStatement(stmtJSON []byte) Statement {
	var s Statement
	if err := json.Unmarshal(stmtJSON, &s); err != nil {
		// The host wrote it from its own Statement: a failure is version
		// skew between host and SDK.
		panic("hoop-codec: the host passed a Statement this SDK cannot read: " + err.Error())
	}
	return s
}

func errorJSON(message string) []byte {
	return mustJSON(errorResult{Error: message})
}

func rewriteJSON(r Rewritten, err error) []byte {
	if err != nil {
		return errorJSON(err.Error())
	}
	return mustJSON(rewriteResult{Bytes: base64.StdEncoding.EncodeToString(r.Bytes), Cells: r.Cells, Rows: r.Rows})
}

func mustJSON(v any) []byte {
	out, err := json.Marshal(v)
	if err != nil {
		panic("hoop-codec: result does not serialize: " + err.Error())
	}
	return out
}

// The required exports. The optional ones are in export_*.go behind
// build tags.

//export describe
func exportDescribe() uint64 {
	return give(current().describe())
}

//export open
func exportOpen(conn, ptr, n uint32) uint32 {
	return current().open(conn, borrow(ptr, n))
}

//export close
func exportClose(conn uint32) {
	current().close(conn)
}

//export decode
func exportDecode(conn, dir, ptr, n uint32) uint64 {
	return give(current().decode(conn, dir, borrow(ptr, n)))
}

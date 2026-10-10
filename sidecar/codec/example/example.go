// Package example is a codec for a protocol that does not exist, written
// to show the smallest thing a codec the registry did not ship has to be.
// It is the template for a custom binary that links its own decoder, and
// the in-process twin of a WASM codec plug-in (codec/wasm): both reach the
// gate the same way, through gate.Config.CodecFactory, and both answer the
// same optional capabilities by type assertion.
//
// The wire format is `x-example`: one message is a length byte followed by
// that many bytes of UTF-8 text. Every message is a statement with
// operation `other`, the text as Text, and metadata x-example.verb=TEXT, so
// a deny_words_list rule matches the text and a metadata rule matches the
// verb. The deny frame is 0xFF, a length byte, the message.
//
// # It does not register itself
//
// Every shipped seam under codec/ calls inspect.Register from init, and
// this package does not. Registration is process-wide and has
// three consequences a test fixture must not have:
//
//   - inspect.Register panics on a duplicate protocol, so a second
//     registration anywhere in the binary (another test, a custom binary
//     that also links this package) is a crash at import time.
//   - inspect.Registered() feeds daemon.Protocols(), the committed
//     daemon/schema.json and the capability header the sidecar sends on
//     its control-plane handshake. An init-time registration here would
//     make every binary importing this package advertise x-example as a
//     protocol it speaks.
//   - A registered protocol is reachable from any config file, so a
//     listener could select it in production.
//
// A lane uses New: hand it to gate.Config.CodecFactory or
// proxy.Config.CodecFactory and the gate never consults the registry. Register
// exists for the custom binary that wants x-example selectable by name;
// call it once, from main or a package init of your own.
package example

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// Protocol is the name an x-example lane declares. Plug-in protocols carry
// the x- prefix so a plug-in can never shadow a built-in name.
const Protocol inspect.Protocol = "x-example"

// denyTag opens a deny frame. 0xFF is not a valid length for a message
// whose text is at most 254 bytes long, so a client that decodes the
// protocol cannot mistake the frame for text.
const denyTag = 0xFF

// maxMessage is the largest message the length byte can describe.
// MaxReassemblyBytes sits a little above it so the generic 8 MiB guard
// never refuses a frame of this protocol.
const maxMessage = 0xFE

// New builds one codec. The codec is stateless beyond what the inspector
// holds for it, but a lane still builds it per connection: that is the
// contract CodecFactory and Register share, and a codec that grows state
// later must not have to change its callers.
func New() inspect.Codec { return &codec{} }

// Register makes x-example selectable by protocol name through the
// registry. See the package comment for why nothing calls it at init.
func Register() {
	inspect.Register(New)
}

type codec struct{}

func (*codec) Protocol() inspect.Protocol { return Protocol }

// Decode parses every complete message in data. A trailing partial message
// is not an error: consumed stops at its first byte and the inspector
// retains the rest for the next read.
func (*codec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	var stmts []inspect.Statement
	consumed := 0
	for consumed < len(data) {
		n := int(data[consumed])
		if n == denyTag {
			// The relay writes deny frames and a peer never sends one, so
			// a deny tag arriving on the wire means the stream is not
			// speaking x-example.
			return nil, 0, errors.New("x-example: deny tag on the wire")
		}
		end := consumed + 1 + n
		if end > len(data) {
			break
		}
		text := data[consumed+1 : end]
		if !utf8.Valid(text) {
			return nil, 0, fmt.Errorf("x-example: message at offset %d is not UTF-8", consumed)
		}
		stmts = append(stmts, inspect.Statement{
			Protocol:  Protocol,
			Direction: dir,
			Text:      string(text),
			Operation: inspect.OpOther,
			Metadata:  map[string]string{"x-example.verb": "TEXT"},
		})
		consumed = end
	}
	return stmts, consumed, nil
}

// DenyFrame implements gate.DenyFramer: a denial reaches the client as a
// frame it can decode. DenyFrame cuts a message longer than the length
// byte can carry, because a truncated reason still beats no reason.
func (*codec) DenyFrame(_ inspect.Direction, message string) []byte {
	if len(message) > maxMessage {
		message = message[:maxMessage]
	}
	frame := make([]byte, 0, 2+len(message))
	frame = append(frame, denyTag, byte(len(message)))
	return append(frame, message...)
}

// Label implements gate.Labeled, the name the control-plane listener form
// shows for a protocol daemon.protocolLabels does not list.
func (*codec) Label() string { return "Example" }

// MaxReassemblyBytes bounds what the inspector holds for one message. The
// protocol's own frame cap is the authority; this keeps the gate's generic
// default from being the number an operator has to reason about.
func (*codec) MaxReassemblyBytes() int { return 1 + maxMessage }

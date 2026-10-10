package wasm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// wireStatement is inspect.Statement as it crosses the ABI, in both
// directions. The embedded struct supplies libhoop's JSON names; the
// shadowing Protocol field takes the key away from it. Decoding a guest
// statement, a non-nil Protocol means the guest wrote the key the host
// owns, and adoptStatement refuses the statement: a module must not
// impersonate another protocol. Encoding a host statement for the guest,
// the nil pointer omits the key, so the guest may hand the same object
// back untouched.
type wireStatement struct {
	inspect.Statement
	Protocol *string `json:"protocol,omitempty"`
}

// decodeResult is DecodeResult of ABI.md.
type decodeResult struct {
	Statements []wireStatement `json:"statements,omitempty"`
	Consumed   int             `json:"consumed"`
	Error      string          `json:"error,omitempty"`
}

// credentialResult is CredentialResult of ABI.md.
type credentialResult struct {
	Credential string         `json:"credential,omitempty"`
	OK         bool           `json:"ok"`
	Statement  *wireStatement `json:"statement,omitempty"`
}

// contentResult is ContentResult of ABI.md.
type contentResult struct {
	Text     string `json:"text,omitempty"`
	CacheKey string `json:"cache_key,omitempty"`
	OK       bool   `json:"ok"`
}

// rewriteResult is RewriteResult of ABI.md. Bytes is base64 in the JSON;
// encoding/json decodes it on the way in.
type rewriteResult struct {
	Bytes []byte `json:"bytes,omitempty"`
	Cells int    `json:"cells,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	Error string `json:"error,omitempty"`
}

// decodeStrict parses one guest payload. It refuses unknown fields, as
// ABI.md says: a misspelt key dropped without an error is a statement a
// policy never sees.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data after the JSON object")
	}
	return nil
}

// adoptStatement turns a guest statement into the host's: refuses the
// protocol key, fills the host's protocol, defaults the direction to the
// call's, and holds the operation to inspect.Operations.
func adoptStatement(ws *wireStatement, proto inspect.Protocol, dir inspect.Direction) (inspect.Statement, error) {
	if ws.Protocol != nil {
		return inspect.Statement{}, fmt.Errorf("statement carries protocol %q; the host fills it", *ws.Protocol)
	}
	s := ws.Statement
	s.Protocol = proto
	switch s.Direction {
	case "":
		s.Direction = dir
	case inspect.FromClient, inspect.FromServer:
	default:
		return inspect.Statement{}, fmt.Errorf("statement direction %q is not client or server", s.Direction)
	}
	if !slices.Contains(inspect.Operations(), s.Operation) {
		return inspect.Statement{}, fmt.Errorf("statement operation %q is not one inspect.Operations lists", s.Operation)
	}
	for _, e := range s.Effects {
		if !slices.Contains(inspect.Operations(), e) {
			return inspect.Statement{}, fmt.Errorf("statement effect %q is not one inspect.Operations lists", e)
		}
	}
	for _, r := range s.Relations {
		if r.Access != inspect.AccessRead && r.Access != inspect.AccessWrite {
			return inspect.Statement{}, fmt.Errorf("relation %q access %q is not read or write", r.Name, r.Access)
		}
	}
	return s, nil
}

// encodeStatement renders a host statement for the guest, protocol
// omitted.
func encodeStatement(s inspect.Statement) ([]byte, error) {
	return json.Marshal(wireStatement{Statement: s})
}

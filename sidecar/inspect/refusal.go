package inspect

// CredentialRefusal is the error a stream filter returns when it refused a
// request's credential before forwarding any of the request.
//
// The filter is the only point that sees a request head before the
// upstream does: the gate judges a statement only once the codec has decoded
// the whole request, body included, and forwards the head before that. So a
// lane that IS the authentication checks the credential in the filter, and
// the gate records the refusal as the identity refusal it would have made.
type CredentialRefusal struct {
	// Request is the method and normalized path of the refused request,
	// for the audit row. Normalized because a vault that fills a token
	// wherever its placeholder appears puts the token in a path too, and
	// the refused caller's token is not one the lane knows to redact:
	// normalization collapses token-shaped segments to "*". No query
	// string: it is not redacted here as the codec would redact it.
	Request string

	// Reason says why the credential names nobody. It never carries the
	// credential.
	Reason error
}

// Error is the client's 403 body.
func (e *CredentialRefusal) Error() string { return "request refused: " + e.Reason.Error() }

// Unwrap returns Reason.
func (e *CredentialRefusal) Unwrap() error { return e.Reason }

// PlacementRefusal is the error a stream filter returns when a request
// carries a credential the filter knows outside the header that credential
// belongs in: in the request line, another header, the body or a trailer.
//
// A vault that fills a secret wherever its placeholder appears (OpenAI's
// does, toward the host it is bound to) turns a token an agent writes into
// a URL into the real token in the URL. The audit trail records the request
// line, and an upstream that echoes a path or query value hands the real
// token back to the agent. Refusing before the upstream sees any of the
// request closes both, where redacting the trail would close only the first.
type PlacementRefusal struct {
	// Request is the method and path of the refused request, for the audit
	// row, with every credential the filter knows replaced by a marker. No
	// query string. Empty when the filter refused bytes it no longer frames
	// as requests.
	Request string

	// Where names the part of the request the credential sat in, for the
	// client's 403 body and the operator: "request line", "header <name>",
	// "body" or "trailer". It never carries a credential.
	Where string
}

// Error is the client's 403 body.
func (e *PlacementRefusal) Error() string {
	return "request refused: a credential this listener knows is in the " + e.Where +
		", outside the header it belongs in; nothing was forwarded"
}

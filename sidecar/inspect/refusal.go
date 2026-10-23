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
	// Request is the method and path of the refused request, for the
	// audit row. No query string: it is not redacted here as the codec
	// would redact it.
	Request string

	// Reason says why the credential names nobody. It never carries the
	// credential.
	Reason error
}

// Error is the client's 403 body.
func (e *CredentialRefusal) Error() string { return "request refused: " + e.Reason.Error() }

// Unwrap returns Reason.
func (e *CredentialRefusal) Unwrap() error { return e.Reason }

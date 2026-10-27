# sidecar/identity/krb5

Nested module on `github.com/jcmturner/gokrb5/v8`. From `init` it registers credential source `kerberos`.

`credentials.kerberos` names one of `cache` (a ccache the platform keeps fresh) or `keytab`, plus `config` (default `/etc/krb5.conf`). `Credential` reads the files on every call, so a rotated ccache or key takes effect at the next login, and returns `User: <principal>` and `GSS`, a `codectypes.GSSInitiator`. With a keytab the pool entry's `principal` is required and must be in the file; with a ccache it defaults to the ccache's principal and must match when set. `Release` destroys the client.

`Initiate(ctx, spn)` fetches the service ticket (a TGT first, from the keytab). The context's first `Step(nil)` returns a SPNEGO NegTokenInit carrying an AP-REQ with mutual authentication required; the second `Step` takes the server's NegTokenResp (or a bare krb5 token), verifies the AP-REP against the authenticator and reports done. Any other reply is an error.

Link it with `_ "github.com/hoophq/hoop/sidecar/identity/krb5"`; `sidecar/cmd` does. A binary without it refuses `source: kerberos` at Validate.

Test: `CGO_ENABLED=0 go test ./...` here, with gokrb5's keytab and ccache fixtures and a forged AP-REP; no KDC. `make test-sidecar` reaches it through `go.work`.

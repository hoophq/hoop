# sidecar/identity/spiffe

Nested module on `github.com/spiffe/go-spiffe/v2`. From `init` it registers:

- identity type `spiffe`: `Verify` validates the JWT-SVID a client sends as its password through the Workload API (`ValidateJWTSVID`, audience `identity.audience`, default `hoop-sidecar`), then refuses an ID outside `identity.trust_domain` or not under `identity.prefix`. The identity is `Subject: spiffe://…` with attributes `trust_domain` and `path`.
- credential source `spiffe`: `Credential` returns the X509-SVID whose ID equals the pool entry's `spiffe_id` as `Cert`, with `user` as the database user. The `X509Source` behind it opens on first use and follows the agent's rotation.

The Workload API address is `credentials.spiffe.socket`, else `SPIFFE_ENDPOINT_SOCKET`, else `unix:///run/spire/sockets/agent.sock`. Every call is bounded to 10 s. Constructors never dial: `-validate` runs where no agent exists.

Link it with `_ "github.com/hoophq/hoop/sidecar/identity/spiffe"`; `sidecar/cmd` does. A binary without it refuses `identity: {type: spiffe}` and `source: spiffe` at Validate.

Test: `CGO_ENABLED=0 go test ./...` here, against an in-process fake Workload API. `make test-sidecar` reaches it through `go.work`.

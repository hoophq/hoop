// The GCS descriptor fetcher is a nested module because it needs GCP OAuth2,
// the same reason analyzer/vertex is one.
//
// The object read itself is one HTTPS GET against the JSON API and needs
// nothing beyond net/http; the credential is what costs a dependency. Under
// Workload Identity, on GCE or with a service-account key, minting a token
// means ADC discovery, a signed JWT assertion and refresh before expiry, and
// golang.org/x/oauth2/google is the well-tested copy of that. cloud.google.com
// /go/storage is deliberately NOT here: a full SDK to perform one GET would
// put dozens of modules behind a fetch of a few megabytes.
//
// A binary that does not import this module resolves no gs:// entry and
// refuses one at config validation.
module github.com/hoophq/hoop/sidecar/descriptors/gcs

go 1.26.8

require (
	github.com/hoophq/hoop/sidecar v0.0.0
	golang.org/x/oauth2 v0.36.0
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/hoophq/hoop/sidecar => ../..

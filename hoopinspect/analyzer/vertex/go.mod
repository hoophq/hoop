// The Vertex analyzer provider is a nested module because it is the only
// provider that needs a dependency.
//
// Anthropic and OpenAI authenticate with a static string in a header, so they
// live in the root against net/http alone. Vertex needs GCP OAuth2: signed
// JWT assertion, token exchange, and refresh before expiry. Reimplementing
// that to preserve a zero-dependency root would be trading a well-tested
// library for a subtle one, so the module boundary takes the cost instead and
// the root stays vendorable without supply-chain review.
//
// A binary that does not import this module links no GCP code at all.
module github.com/hoophq/hoop/hoopinspect/analyzer/vertex

go 1.26.5

require (
	github.com/hoophq/hoop/hoopinspect v0.0.0
	golang.org/x/oauth2 v0.32.0
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
)

replace github.com/hoophq/hoop/hoopinspect => ../..

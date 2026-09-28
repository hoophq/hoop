// Package vertex implements Claude, Gemini and the Model Garden open models
// on Google Vertex AI as an analyzer provider.
//
// It is a separate module because it is the one provider that needs a
// dependency. Anthropic, OpenAI and Gemini authenticate with a static string
// in a header; Vertex authenticates with a GCP OAuth2 bearer minted from a
// service-account key and refreshed before it expires. Signed JWT assertion
// and token exchange are not worth reimplementing to save a go.mod, so this
// module takes golang.org/x/oauth2 and the root never links it.
//
// The `publisher` extra picks the model family, and with it the wire format.
// Claude is the Anthropic Messages API with three transport changes: the
// model moves into the URL, the API version moves into the body, and auth
// becomes a bearer. Gemini is the generateContent API the analyzer/gemini
// package speaks, with the same bearer in place of an API key. The Model
// Garden open models (Llama, DeepSeek, Qwen, gpt-oss) use the Chat
// Completions API the analyzer/openai package speaks, on Vertex's
// OpenAI-compatible endpoint. This package imports the request and response
// encoders from analyzer/anthropic, analyzer/gemini and analyzer/openai and
// keeps no copy of them.
//
// # Credentials
//
// Two modes, and the first is strongly preferred:
//
//   - Application Default Credentials, when credentials_file is unset. On GKE
//     with Workload Identity there is then NO credential on disk at all: the
//     pod's identity is the credential, and there is nothing to leak, rotate
//     or mode-check.
//   - A service-account key file, for hosts outside GCP.
package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/analyzer/anthropic"
	"github.com/hoophq/hoop/sidecar/analyzer/gemini"
	"github.com/hoophq/hoop/sidecar/analyzer/openai"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Name is the config value that selects this provider.
const Name = "vertex"

// scope is the OAuth scope a Vertex prediction call requires. It pairs with
// the roles/aiplatform.user IAM role on the service account.
const scope = "https://www.googleapis.com/auth/cloud-platform"

const defaultMaxTokens = 1024

// Extra keys this provider reads from the config's analyzer section.
const (
	KeyProject   = "project"
	KeyRegion    = "region"
	KeyPublisher = "publisher"
)

// Publisher values for KeyPublisher. Empty means PublisherAnthropic, which
// is what every config written before the key existed meant.
const (
	// PublisherAnthropic serves Claude through rawPredict.
	PublisherAnthropic = "anthropic"

	// PublisherGoogle serves Gemini through generateContent.
	PublisherGoogle = "google"

	// PublisherOpenAPI serves the Model Garden open models through Vertex's
	// OpenAI-compatible Chat Completions endpoint. All publishers share
	// that endpoint, so the model name carries the real publisher:
	// `meta/llama-4-maverick-17b-128e-instruct-maas`. The value matches the
	// `endpoints/openapi` URL segment and has no relation to OpenAI.
	//
	// The classifier forces a tool call, so a model without function
	// calling fails each statement with the same "called no risk tool"
	// error the other providers return.
	PublisherOpenAPI = "openapi"
)

func init() {
	analyzer.Register(Name, func(opts analyzer.Options) (analyzer.Provider, error) {
		project := strings.TrimSpace(opts.Extra[KeyProject])
		if project == "" {
			return nil, fmt.Errorf("analyzer/vertex: no project configured")
		}
		region := strings.TrimSpace(opts.Extra[KeyRegion])
		if region == "" {
			return nil, fmt.Errorf("analyzer/vertex: no region configured")
		}
		if opts.Model == "" {
			return nil, fmt.Errorf("analyzer/vertex: no model configured")
		}
		publisher := strings.TrimSpace(opts.Extra[KeyPublisher])
		if publisher == "" {
			publisher = PublisherAnthropic
		}
		switch publisher {
		case PublisherAnthropic, PublisherGoogle:
		case PublisherOpenAPI:
			// Vertex routes the shared endpoint on the prefix. This check
			// reports a bare name at config load, where the operator is
			// watching, instead of as an API error on the first statement.
			if !strings.Contains(opts.Model, "/") {
				return nil, fmt.Errorf("analyzer/vertex: publisher %q needs the model as <publisher>/<model>, e.g. meta/llama-4-maverick-17b-128e-instruct-maas; got %q",
					PublisherOpenAPI, opts.Model)
			}
		default:
			// Refused at config load, where the operator is watching. A
			// fallback would send a Gemini model to the anthropic path
			// and the resulting 404 reads like a typo in the model name.
			return nil, fmt.Errorf("analyzer/vertex: unknown publisher %q (want %q, %q or %q)",
				publisher, PublisherAnthropic, PublisherGoogle, PublisherOpenAPI)
		}
		maxTokens := opts.MaxOutputTokens
		if maxTokens <= 0 {
			maxTokens = defaultMaxTokens
		}

		p := &Provider{
			project:   project,
			region:    region,
			publisher: publisher,
			model:     opts.Model,
			endpoint:  opts.Endpoint,
			maxTokens: maxTokens,
			saJSON:    opts.Credential,
			client:    &http.Client{},
		}
		return p, nil
	})
}

// Provider classifies statements with Claude or Gemini on Vertex AI.
type Provider struct {
	project   string
	region    string
	publisher string
	model     string
	endpoint  string // overrides the derived URL; empty derives it
	maxTokens int
	saJSON    analyzer.Secret

	client *http.Client

	// tokenOnce builds the token source lazily and exactly once.
	//
	// Once, because oauth2.TokenSource caches the token internally and
	// refreshes it shortly before expiry: building a fresh source per
	// request would mint a fresh token per request, turning every
	// classification into two round trips and hammering Google's token
	// endpoint. Lazily, because CredentialsFromJSON parses without network
	// I/O but ADC discovery may probe the metadata server, and that must
	// not happen during config validation on a host with no network.
	tokenOnce sync.Once
	tokenSrc  oauth2.TokenSource
	tokenErr  error
}

// Name implements analyzer.Provider.
func (p *Provider) Name() string { return Name }

// tokenSource resolves the credential once.
//
// The source is built on context.Background, never on the caller's ctx.
// oauth2 keeps the context it was built with and uses it for every refresh
// afterwards. Verify runs under a 15-second startup context that the daemon
// cancels as soon as validation returns; a source built on it mints the
// first token and fails every refresh an hour later with "context canceled".
// The caller's ctx still bounds the Vertex HTTP request in Classify, which is
// the call that has a deadline to respect.
func (p *Provider) tokenSource() (oauth2.TokenSource, error) {
	p.tokenOnce.Do(func() {
		ctx := context.Background()
		var creds *google.Credentials
		var err error
		if p.saJSON.IsZero() {
			// Application Default Credentials: Workload Identity, a
			// GCE/Cloud Run service account, or gcloud on a laptop.
			creds, err = google.FindDefaultCredentials(ctx, scope)
			if err != nil {
				p.tokenErr = fmt.Errorf(
					"analyzer/vertex: no credentials_file set and no application "+
						"default credentials found: %w", err)
				return
			}
		} else {
			creds, err = google.CredentialsFromJSON(ctx, p.saJSON.Bytes(), scope)
			if err != nil {
				// The message never includes the error's payload
				// verbatim beyond what the library says; a malformed
				// key file's parse error can quote its contents.
				p.tokenErr = fmt.Errorf("analyzer/vertex: invalid service account key: %w", err)
				return
			}
		}
		p.tokenSrc = creds.TokenSource
	})
	return p.tokenSrc, p.tokenErr
}

// Verify mints one token so a bad credential fails at startup.
//
// Without it the first sign of a wrong key, a clock skew or a missing
// roles/aiplatform.user binding is a denied statement in production, at the
// worst possible moment. `-validate` calls this.
//
// It deliberately does NOT call the model: that would cost money on every
// config check and would not test anything minting a token does not.
func (p *Provider) Verify(ctx context.Context) error {
	ts, err := p.tokenSource()
	if err != nil {
		return err
	}
	// ctx bounds this one mint. oauth2's TokenSource takes no context, so
	// the bound is a watchdog around the call rather than a deadline on it.
	done := make(chan error, 1)
	go func() {
		_, err := ts.Token()
		done <- err
	}()
	select {
	case <-ctx.Done():
		return fmt.Errorf("analyzer/vertex: could not mint a GCP access token: %w", ctx.Err())
	case err = <-done:
	}
	if err != nil {
		return fmt.Errorf("analyzer/vertex: could not mint a GCP access token "+
			"(check the service account, its roles/aiplatform.user binding, and the host clock): %w", err)
	}
	return nil
}

// url builds the prediction endpoint for the configured model.
//
// Claude is served by rawPredict, which passes the Anthropic body through.
// Gemini is served by generateContent, Google's own method. The publisher
// segment of the path matches the config key by design: Google names its
// model catalog by publisher, and an operator reading a 404 in the Cloud
// Console log sees the same word they wrote in the config. The open models
// share one Chat Completions endpoint with no model in its path. The body
// carries the model, and `openapi` is again the word in the path.
//
// The "global" region is spelled differently from a regional one: it uses the
// unprefixed host. Getting this wrong yields a DNS failure rather than an API
// error, which reads like a network problem and sends an operator to the
// wrong place.
func (p *Provider) url() string {
	if p.endpoint != "" {
		return p.endpoint
	}
	host := p.region + "-aiplatform.googleapis.com"
	if p.region == "global" {
		host = "aiplatform.googleapis.com"
	}
	base := fmt.Sprintf("https://%s/v1/projects/%s/locations/%s", host, p.project, p.region)
	switch p.publisher {
	case PublisherOpenAPI:
		return base + "/endpoints/openapi/chat/completions"
	case PublisherGoogle:
		return base + "/publishers/google/models/" + p.model + ":generateContent"
	default:
		return base + "/publishers/anthropic/models/" + p.model + ":rawPredict"
	}
}

// Classify implements analyzer.Provider.
func (p *Provider) Classify(ctx context.Context, systemPrompt, content string) (*analyzer.Result, error) {
	ts, err := p.tokenSource()
	if err != nil {
		return nil, err
	}
	token, err := ts.Token()
	if err != nil {
		// Distinguished from an API failure on purpose: an IAM or clock
		// problem and a model outage need different people.
		return nil, fmt.Errorf("analyzer/vertex: minting GCP access token: %w", err)
	}

	body, err := p.encode(systemPrompt, content)
	if err != nil {
		return nil, fmt.Errorf("analyzer/vertex: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url(), strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("analyzer/vertex: building request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+token.AccessToken)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("analyzer/vertex: %w", err)
	}
	defer resp.Body.Close()

	// Each publisher returns the document its own package parses,
	// including the bounded-drain rule that keeps a provider's error body
	// out of the relay's logs.
	switch p.publisher {
	case PublisherGoogle:
		return gemini.ParseResponse("analyzer/"+Name, resp)
	case PublisherOpenAPI:
		return openai.ParseResponse("analyzer/"+Name, resp)
	default:
		return anthropic.ParseResponse("analyzer/"+Name, resp)
	}
}

// encode renders the request body for the configured publisher.
//
// forVertex=true on the Anthropic encoder moves the model out of the body and
// the API version into it. The Gemini encoder never carries the model: every
// Gemini URL names it in the path. forVertex=true on the OpenAI encoder
// spells the output limit max_tokens, the only name Vertex documents.
func (p *Provider) encode(systemPrompt, content string) ([]byte, error) {
	switch p.publisher {
	case PublisherGoogle:
		return json.Marshal(gemini.BuildRequest(p.maxTokens, systemPrompt, content))
	case PublisherOpenAPI:
		return json.Marshal(openai.BuildRequest(p.model, p.maxTokens, systemPrompt, content, true))
	default:
		return json.Marshal(anthropic.BuildRequest(p.model, p.maxTokens, systemPrompt, content, true))
	}
}

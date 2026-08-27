// Package vertex implements Claude on Google Vertex AI as an analyzer
// provider.
//
// It is a separate module because it is the one provider that needs a
// dependency. Anthropic and OpenAI authenticate with a static string in a
// header; Vertex authenticates with a GCP OAuth2 bearer minted from a
// service-account key and refreshed before it expires. Signed JWT assertion
// and token exchange are not worth reimplementing to save a go.mod, so this
// module takes golang.org/x/oauth2 and the root never links it.
//
// The wire format is the Anthropic Messages API with three changes, all of
// them transport: the model moves into the URL, the API version moves into
// the body, and auth becomes a bearer. The request and response encoders are
// therefore imported from analyzer/anthropic rather than copied.
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

	"github.com/hoophq/hoop/hoopinspect/analyzer"
	"github.com/hoophq/hoop/hoopinspect/analyzer/anthropic"
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
	KeyProject = "project"
	KeyRegion  = "region"
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
		maxTokens := opts.MaxOutputTokens
		if maxTokens <= 0 {
			maxTokens = defaultMaxTokens
		}

		p := &Provider{
			project:   project,
			region:    region,
			model:     opts.Model,
			endpoint:  opts.Endpoint,
			maxTokens: maxTokens,
			saJSON:    opts.Credential,
			client:    &http.Client{},
		}
		return p, nil
	})
}

// Provider classifies statements with Claude on Vertex AI.
type Provider struct {
	project   string
	region    string
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
func (p *Provider) tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	p.tokenOnce.Do(func() {
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
	ts, err := p.tokenSource(ctx)
	if err != nil {
		return err
	}
	if _, err := ts.Token(); err != nil {
		return fmt.Errorf("analyzer/vertex: could not mint a GCP access token "+
			"(check the service account, its roles/aiplatform.user binding, and the host clock): %w", err)
	}
	return nil
}

// url builds the rawPredict endpoint for the configured model.
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
	return fmt.Sprintf(
		"https://%s/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:rawPredict",
		host, p.project, p.region, p.model)
}

// Classify implements analyzer.Provider.
func (p *Provider) Classify(ctx context.Context, systemPrompt, content string) (*analyzer.Result, error) {
	ts, err := p.tokenSource(ctx)
	if err != nil {
		return nil, err
	}
	token, err := ts.Token()
	if err != nil {
		// Distinguished from an API failure on purpose: an IAM or clock
		// problem and a model outage need different people.
		return nil, fmt.Errorf("analyzer/vertex: minting GCP access token: %w", err)
	}

	// forVertex=true moves the model out of the body and the API version
	// into it. Everything else is the Anthropic request verbatim.
	body, err := json.Marshal(anthropic.BuildRequest(p.model, p.maxTokens, systemPrompt, content, true))
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

	// Vertex returns the same document as the Messages API, so the
	// Anthropic parser handles it, including the bounded-drain rule that
	// keeps a provider's error body out of the relay's logs.
	return anthropic.ParseResponse("analyzer/"+Name, resp)
}

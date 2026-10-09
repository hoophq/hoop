// Package hoop is the analyzer provider for the model Hoop hosts for the
// free tier (DEP-161).
//
// The hosted model speaks the OpenAI Chat Completions dialect, so this
// provider is the openai provider with two differences: it needs no API key,
// and every request is signed by libhoop's hostedllm transport, which the
// proxy in front of the model verifies.
package hoop

import (
	"fmt"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/analyzer/openai"
	"github.com/hoophq/libhoop/hostedllm"
)

// Name is the registered name. Config selects it with the analyzer section's
// use_hoop_llm_provider flag, not by provider name.
const Name = "hoop"

func init() {
	analyzer.Register(Name, func(opts analyzer.Options) (analyzer.Provider, error) {
		if opts.Endpoint == "" {
			opts.Endpoint = analyzer.HostedEndpoint
		}
		if opts.Model == "" {
			opts.Model = analyzer.HostedModel
		}
		if !opts.Credential.IsZero() {
			return nil, fmt.Errorf("analyzer/%s: takes no credentials_file; requests are signed", Name)
		}

		base := opts.Client()
		signed := *base
		signed.Transport = &hostedllm.Transport{Base: base.Transport}
		opts.HTTPClient = &signed
		// The openai provider requires a key and sends it as a bearer
		// token. The proxy authenticates by signature and strips it.
		opts.Credential = analyzer.NewSecret([]byte(Name))

		p, err := openai.New(Name, opts)
		if err != nil {
			return nil, err
		}
		return p, nil
	})
}

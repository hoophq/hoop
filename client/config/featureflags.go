package clientconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
)

type serverInfoFlags struct {
	FeatureFlags map[string]bool `json:"feature_flags"`
}

// StatusError is a non-200 answer from /api/serverinfo.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("failed fetching serverinfo, status=%d, body=%s", e.Code, e.Body)
}

// FetchFeatureFlags retrieves the feature flags from the gateway's /serverinfo endpoint.
func FetchFeatureFlags(conf *Config) (map[string]bool, error) {
	return FetchFeatureFlagsContext(context.Background(), conf)
}

// FetchFeatureFlagsContext is FetchFeatureFlags bound to ctx, so a caller can
// set a deadline.
func FetchFeatureFlagsContext(ctx context.Context, conf *Config) (map[string]bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/serverinfo", conf.ApiURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("authorization", fmt.Sprintf("Bearer %s", conf.Token))
	if conf.IsApiKey() {
		req.Header.Set("Api-Key", conf.Token)
	}
	resp, err := httpclient.NewHttpClient(conf.TlsCA()).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &StatusError{Code: resp.StatusCode, Body: string(body)}
	}
	var si serverInfoFlags
	if err := json.NewDecoder(resp.Body).Decode(&si); err != nil {
		return nil, fmt.Errorf("failed decoding serverinfo: %v", err)
	}
	return si.FeatureFlags, nil
}

// IsFeatureEnabled fetches the current feature flags from the gateway and
// returns whether the given flag is enabled for the caller's org.
func IsFeatureEnabled(conf *Config, name string) bool {
	flags, err := FetchFeatureFlags(conf)
	if err != nil {
		log.Debugf("failed fetching feature flags: %v", err)
		return false
	}
	return flags[name]
}

package clientconfig

import (
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

// FetchFeatureFlags retrieves the feature flags from the gateway's /serverinfo endpoint.
func FetchFeatureFlags(conf *Config) (map[string]bool, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/serverinfo", conf.ApiURL), nil)
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
		return nil, fmt.Errorf("failed fetching serverinfo, status=%d, body=%s", resp.StatusCode, string(body))
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

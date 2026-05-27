package upgrade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hoophq/hoop/common/httpclient"
)

// GatewayInfo is the subset of /api/serverinfo we care about.
type GatewayInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit_sha"`
}

// FetchGatewayInfo calls GET <apiURL>/api/serverinfo and decodes the
// version fields. The caller must pass a valid bearer token; the endpoint
// is authenticated.
func FetchGatewayInfo(apiURL, bearerToken, tlsCA string) (*GatewayInfo, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("missing api_url in hoop client config; run `hoop login` first")
	}
	if bearerToken == "" {
		return nil, fmt.Errorf("missing token in hoop client config; run `hoop login` first")
	}
	url := fmt.Sprintf("%s/api/serverinfo", strings.TrimRight(apiURL, "/"))
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed building request to %s: %w", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	resp, err := httpclient.NewHttpClient(tlsCA).Do(req)
	if err != nil {
		return nil, httpclient.HumanizeNetError(apiURL, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var gi GatewayInfo
		if err := json.NewDecoder(resp.Body).Decode(&gi); err != nil {
			return nil, fmt.Errorf("failed decoding serverinfo response: %w", err)
		}
		if gi.Version == "" {
			return nil, fmt.Errorf("gateway did not return a version (apiURL=%s)", apiURL)
		}
		return &gi, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("gateway rejected the credentials (%s); run `hoop login` and retry", resp.Status)
	default:
		return nil, fmt.Errorf("unexpected response from %s: status=%d", url, resp.StatusCode)
	}
}

package idp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const gSuiteGroupsURL = "https://www.googleapis.com/admin/directory/v1/groups"

type gsuiteGroups struct {
	NextPageToken string             `json:"nextPageToken"`
	Groups        []gsuiteGroupEntry `json:"groups"`
}

type gsuiteGroupEntry struct {
	Email string `json:"email"`
}

func (p *Provider) fetchGsuiteGroups(accessToken, email string) ([]string, error) {
	apiURL := fmt.Sprintf("%s?userKey=%s", gSuiteGroupsURL, email)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating request to gsuite, reason=%v", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing request to gsuite, reason=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unable to obtain groups from gsuite, status=%v, body=%v",
			resp.StatusCode, string(body))
	}
	var response gsuiteGroups
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed decoding gsuite response, reason=%v", err)
	}
	var groups []string
	for _, group := range response.Groups {
		groups = append(groups, group.Email)
	}
	return groups, nil
}

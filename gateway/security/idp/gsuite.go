package idp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	// https://developers.google.com/admin-sdk/directory/reference/rest/v1/groups/list
	gSuiteGroupsURL   = "https://www.googleapis.com/admin/directory/v1/groups"
	defaultMaxPages   = 3
	defaultMaxResults = 200
)

type gsuiteGroups struct {
	NextPageToken string             `json:"nextPageToken"`
	Groups        []gsuiteGroupEntry `json:"groups"`
}

type gsuiteGroupEntry struct {
	Email string `json:"email"`
}

func (p *Provider) fetchGsuiteGroups(accessToken, email string) ([]string, error) {
	var groups []string
	var nextPageToken string

	for count := 0; ; count++ {
		if count > defaultMaxPages {
			return nil, fmt.Errorf("reached max pagination (%v) fetching Gsuite Groups", defaultMaxPages)
		}
		response, err := p.fetchGroupsPage(accessToken, email, nextPageToken)
		if err != nil {
			return nil, fmt.Errorf("page=%v, %v", count, err)
		}
		for _, entry := range response.Groups {
			groups = append(groups, entry.Email)
		}
		if response.NextPageToken != "" {
			nextPageToken = response.NextPageToken
			continue
		}
		break
	}
	return groups, nil
}

func (p *Provider) fetchGroupsPage(accessToken, email, pageToken string) (*gsuiteGroups, error) {
	apiURL := fmt.Sprintf("%s?userKey=%s&pageToken=%s&maxResults=%v",
		gSuiteGroupsURL, email, pageToken, defaultMaxResults)
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
	return &response, nil
}

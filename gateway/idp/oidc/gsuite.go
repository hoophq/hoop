package oidcprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hoophq/hoop/common/log"
)

const (
	// https://cloud.google.com/identity/docs/reference/rest/v1/groups.memberships/searchDirectGroups
	cloudIdentityURL   = "https://cloudidentity.googleapis.com/v1/groups/-/memberships:searchDirectGroups"
	defaultMaxPages    = 3
	defaultMaxPageSize = 200
)

type gsuiteGroups struct {
	NextPageToken string            `json:"nextPageToken"`
	Memberships   []membershipGroup `json:"memberships"`
}

type groupKey struct {
	ID string `json:"id"`
}

type groupRole struct {
	Name string `json:"name"`
}

type membershipGroup struct {
	Membership  string      `json:"membership"`
	Roles       []groupRole `json:"roles"`
	Group       string      `json:"group"`
	GroupKey    groupKey    `json:"groupKey"`
	DisplayName string      `json:"displayName"`
	Description string      `json:"description"`
}

func (g gsuiteGroups) String() string {
	var memberStr string
	for _, m := range g.Memberships {

		memberStr += fmt.Sprintf("groupid=%v,group=%v,roles=%v,display_name=%v | ",
			m.Group, m.GroupKey.ID, m.Roles, m.DisplayName)
	}
	memberStr = strings.TrimSuffix(memberStr, " | ")
	return fmt.Sprintf("page_token=%v, members=%v, %v", g.NextPageToken != "", len(g.Memberships), memberStr)
}

func (p *provider) fetchGsuiteGroups(accessToken, email string) (groups []string, mustSync bool, err error) {
	if !p.mustFetchGsuiteGroups {
		return
	}
	var nextPageToken string
	for count := 0; ; count++ {
		if count > defaultMaxPages {
			return nil, false, fmt.Errorf("reached max pagination (%v) fetching Gsuite Groups", defaultMaxPages)
		}
		response, err := p.fetchGroupsPage(accessToken, email, nextPageToken)
		if err != nil {
			return nil, false, fmt.Errorf("page=%v, %v", count, err)
		}
		log.Debugf("cloud identity response obtaining groups, %v", response.String())
		for _, entry := range response.Memberships {
			if entry.GroupKey.ID == "" {
				continue
			}
			groups = append(groups, entry.GroupKey.ID)
		}
		if response.NextPageToken != "" {
			nextPageToken = response.NextPageToken
			continue
		}
		break
	}
	return groups, true, nil
}

func (p *provider) fetchGroupsPage(accessToken, email, pageToken string) (*gsuiteGroups, error) {
	apiURL := fmt.Sprintf("%s?query=member_key_id=='%s'&pageSize=%v&pageToken=%v",
		cloudIdentityURL, email, defaultMaxPageSize, pageToken)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating request to gsuite, reason=%v", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing request to cloud identity api, reason=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unable to obtain groups from cloud identity api, status=%v, body=%v",
			resp.StatusCode, string(body))
	}
	var response gsuiteGroups
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed decoding cloud identity response, reason=%v", err)
	}
	return &response, nil
}

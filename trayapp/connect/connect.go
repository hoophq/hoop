package connect

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Connection struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

type UserInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// type Session struct {
// 	ID         string `json:"id"`
// 	Connection string `json:"connection"`
// 	Verb       string `json:"verb"`
// }

// type SessionItems struct {
// 	Data []Session `json:"data"`
// }

// func getUserInfo(apiURL, accessToken string) (*UserInfo, error) {
// 	url := fmt.Sprintf("%s/api/userinfo", apiURL)
// 	req, err := http.NewRequest("GET", url, nil)
// 	if err != nil {
// 		return nil, err
// 	}
// 	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
// 	resp, err := http.DefaultClient.Do(req)
// 	if err != nil {
// 		return nil, err
// 	}
// 	if resp.StatusCode == 200 {
// 		var uinfo UserInfo
// 		if err := json.NewDecoder(resp.Body).Decode(&uinfo); err != nil {
// 			return nil, fmt.Errorf("failed decoding userinfo: %v", err)
// 		}
// 		return &uinfo, nil
// 	}
// 	data, _ := io.ReadAll(resp.Body)
// 	return nil, fmt.Errorf("failed fetching user info, status=%v, response=%v", resp.StatusCode, string(data))
// }

func List(apiURL, accessToken string) ([]*Connection, error) {
	url := fmt.Sprintf("%s/api/connections", apiURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 200 {
		var connections []*Connection
		if err := json.NewDecoder(resp.Body).Decode(&connections); err != nil {
			return nil, fmt.Errorf("failed decoding userinfo: %v", err)
		}
		var newItems []*Connection
		for _, conn := range connections {
			if conn.Type == "database" || conn.Subtype == "tcp" {
				newItems = append(newItems, conn)
			}
		}
		return newItems, nil
	}
	data, _ := io.ReadAll(resp.Body)
	return nil, fmt.Errorf("failed fetching user info, status=%v, response=%v", resp.StatusCode, string(data))
}

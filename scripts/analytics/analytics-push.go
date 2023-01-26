package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type (
	httpClient struct {
		sourceHost      string
		destinationHost string
		client          http.Client
	}

	Context struct {
		Org  Org
		User User
	}

	Org struct {
		Id   string `json:"id"`
		Name string `json:"name"`
	}

	User struct {
		Id     string   `json:"xt/id"`
		Org    string   `json:"user/org"`
		Name   string   `json:"user/name"`
		Email  string   `json:"user/email"`
		Status string   `json:"user/status"`
		Groups []string `json:"user/groups"`
	}
)

var c = httpClient{
	sourceHost:      "http://127.0.0.1:8999/%s",
	destinationHost: "https://api.segment.io/%s",
	client:          http.Client{Timeout: 3 * time.Second},
}

var segmentKey = os.Getenv("SEGMENT_KEY")

func main() {
	if segmentKey == "" {
		fmt.Println("please export SEGMENT_KEY. exiting...")
		os.Exit(1)
	}

	contexts, err := buildContexts()
	if err != nil {
		fmt.Printf("Failed with error: %s\n", err.Error())
		os.Exit(1)
	}

	if err := pushContexts(contexts); err != nil {
		fmt.Printf("Failed with error: %s\n", err.Error())
		os.Exit(1)
	}

	fmt.Println("Done")
}

func buildContexts() ([]Context, error) {
	contexts := make([]Context, 0)
	orgs, err := getOrgs()
	if err != nil {
		return nil, err
	}

	for _, o := range orgs {
		users, err := getUsers(o)
		if len(users) == 0 {
			continue
		}
		if err != nil {
			return nil, err
		}

		for _, u := range users {
			ctx := Context{
				Org:  o,
				User: u,
			}
			contexts = append(contexts, ctx)
		}
	}

	return contexts, nil
}

func getOrgs() ([]Org, error) {
	ednBody := []byte(`
		{:query 
		 {:find [i s]
		  :where [[p :org/name s]
                  [p :xt/id i]]}}`)

	var result []any
	result, err := getItems(ednBody, result)
	if err != nil {
		return nil, err
	}

	orgs := make([]Org, 0)
	for _, r := range result {
		o := r.([]any)
		org := Org{
			Id:   o[0].(string),
			Name: o[1].(string),
		}
		orgs = append(orgs, org)
	}

	return orgs, nil
}

func getUsers(org Org) ([]User, error) {
	fmt.Printf("Getting users for %s...\n", org.Name)
	ednBody := []byte(fmt.Sprintf(`
		{:query 
		 {:find [(pull ?user [*])]
			:in [orgID]
		  :where [[?user :user/org orgID]]}
		  :in-args ["%s"]}`, org.Id))

	var result []any
	result, err := getItems(ednBody, result)
	if err != nil {
		return nil, err
	}

	users := make([]User, 0)
	if len(result) == 0 {
		return users, nil
	}

	for _, rr := range result {
		rrr := rr.([]any)
		for _, u := range rrr {
			us := u.(map[string]any)
			groups := make([]string, 0)
			for _, g := range us["user/groups"].([]any) {
				groups = append(groups, g.(string))
			}
			user := User{
				Id:     us["xt/id"].(string),
				Org:    us["user/org"].(string),
				Name:   us["user/name"].(string),
				Email:  us["user/email"].(string),
				Status: us["user/status"].(string),
				Groups: groups,
			}
			users = append(users, user)
		}
	}

	return users, nil
}

func getItems(ednQuery []byte, obj []any) ([]any, error) {
	bodyReader := bytes.NewReader(ednQuery)

	requestURL := fmt.Sprintf(c.sourceHost, "_xtdb/query")
	req, err := http.NewRequest(http.MethodPost, requestURL, bodyReader)
	if err != nil {
		return nil, nil
	}

	req.Header.Set("Content-type", "application/edn")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, err
	}

	return obj, nil
}

func pushContexts(contexts []Context) error {
	for _, ctx := range contexts {
		err := pushIdentify(ctx)
		err = pushGroup(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

func pushIdentify(ctx Context) error {
	url := fmt.Sprintf(c.destinationHost, "v1/identify")
	p := map[string]any{
		"userId": ctx.User.Id,
		"traits": map[string]any{
			"email":    ctx.User.Email,
			"name":     ctx.User.Name,
			"status":   ctx.User.Status,
			"is-admin": ctx.User.IsAdmin(),
			"groups":   ctx.User.Groups,
		},
	}

	payload, _ := json.Marshal(p)
	return pushAnalytics(url, payload)
}

func pushGroup(ctx Context) error {
	url := fmt.Sprintf(c.destinationHost, "v1/group")
	p := map[string]any{
		"userId":  ctx.User.Id,
		"groupId": ctx.Org.Id,
		"traits": map[string]any{
			"name": ctx.Org.Name,
		},
	}

	payload, _ := json.Marshal(p)
	return pushAnalytics(url, payload)
}

func pushAnalytics(url string, payload []byte) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(string(payload)))
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s:", segmentKey)
	b64Key := base64.StdEncoding.EncodeToString([]byte(key))

	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", fmt.Sprintf("Basic %s", b64Key))

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (user *User) IsAdmin() bool {
	return isInList("admin", user.Groups)
}

func isInList(item string, items []string) bool {
	for _, i := range items {
		if i == item {
			return true
		}
	}
	return false
}

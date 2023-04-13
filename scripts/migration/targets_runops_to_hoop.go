package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// this script requires a CSV file
// containing all the targets to be migrated
//
// required args are: org-name csv-path:
// go run . ebanx path_to_file.csv
//
// csv MUST be SEMI-COLON separated (;) because
// the "reviewers" field are a comma separated
// field that will mess the parsing
//
// required fields in this order are:
// [name, type, review_type, reviewers, redact]
//
// select name,type,review_type,reviewers,redact
// from targets where org = '{}' and status = 'active'
// order by name;
//
// !!! THIS SCRIPT IS NOT IDEMPOTENT !!!
// running it twice will duplicate the connections

type (
	target struct {
		Id         string   `json:"xt/id"`
		Name       string   `json:"connection/name"`
		Type       string   `json:"connection/type"`
		Agent      string   `json:"connection/agent"`
		Org        string   `json:"connection/org"`
		ReviewType string   `json:"-"`
		Reviewers  []string `json:"-"`
		Redact     string   `json:"-"`
	}

	pluginConfig struct {
		Id           string   `json:"xt/id"`
		ConnectionID string   `json:"plugin-connection/id"`     // connection id
		Config       []string `json:"plugin-connection/config"` // list of groups
	}

	pluginConfigSimpleName struct {
		ConnectionID string   `json:"id"`     // connection id
		Config       []string `json:"config"` // list of groups
	}

	plugin struct {
		Id            string                   `json:"xt/id"`
		Org           string                   `json:"plugin/org"`
		Name          string                   `json:"plugin/name"`
		Priority      int                      `json:"plugin/priority"`
		InstalledBy   string                   `json:"plugin/installed-by"`
		ConnectionIDs []string                 `json:"plugin/connection-ids"`
		Connections   []pluginConfigSimpleName `json:"plugin/connections"`
	}

	httpClient struct {
		host   string
		client http.Client
	}
)

var c = httpClient{
	host:   "http://127.0.0.1:3000/%s",
	client: http.Client{Timeout: 3 * time.Second},
}

func main() {
	if len(os.Args) <= 2 {
		fmt.Println("Must provide an argument with the Org and CSV path")
		os.Exit(1)
	}

	org := os.Args[1]
	if org == "" {
		fmt.Println("Org not provided. Exiting...")
		os.Exit(1)
	}

	csvPath := os.Args[2]
	file, err := os.Open(csvPath)
	if err != nil {
		fmt.Printf("Failed to load CSV file, err=%v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	r := csv.NewReader(file)
	r.FieldsPerRecord = -1
	r.Comma = ';'

	records, err := r.ReadAll()
	if err != nil {
		fmt.Printf("Failed to read CSV records, err=%v\n", err)
		os.Exit(1)
	}

	orgID, err := getOrgID(org)
	if err != nil {
		fmt.Printf("Failed to get org, err=%v\n", err)
		os.Exit(1)
	}

	agentIDs, err := getAgents(orgID)
	if err != nil {
		fmt.Printf("Failed to get org, err=%v\n", err)
		os.Exit(1)
	}

	if len(agentIDs) == 0 {
		fmt.Println("To migrate connections there must be a configured agent. No agents found.")
		os.Exit(1)
	}

	installedBy, err := getUsers(orgID)
	if err != nil {
		fmt.Printf("Failed to get org, err=%v\n", err)
		os.Exit(1)
	}

	items := make([]any, 0)
	targets := buildTargets(records, orgID, agentIDs[0])
	for _, v := range targets {
		items = append(items, v)
	}

	plugins, pluginsConfig := buildPlugins(targets, orgID, installedBy[0])
	for _, v := range plugins {
		items = append(items, v)
	}
	for _, v := range pluginsConfig {
		items = append(items, v)
	}

	if err := submitTx(items); err != nil {
		fmt.Printf("Failed to persist targets, err=%v\n", err)
		os.Exit(1)
	}
}

func buildTargets(records [][]string, orgID, agentID string) []target {
	targets := make([]target, 0)
	for i, row := range records {
		if i == 0 {
			continue
		}
		targets = append(targets, target{
			Id:         uuid.NewString(),
			Name:       row[0],
			ReviewType: row[2],
			Reviewers:  strings.Split(row[3], ","),
			Redact:     row[4],
			Agent:      agentID,
			Org:        orgID,
			Type:       "command-line",
		})
	}
	return targets
}

func buildPlugins(targets []target, orgID string, installedBy string) ([]plugin, []pluginConfig) {
	plugins := make([]plugin, 0)
	pluginsConfig := make([]pluginConfig, 0)

	auditPlugin := plugin{
		Id:            uuid.NewString(),
		Org:           orgID,
		Name:          "audit",
		Priority:      0,
		InstalledBy:   installedBy,
		ConnectionIDs: make([]string, 0),
		Connections:   make([]pluginConfigSimpleName, 0),
	}
	dlpPlugin := plugin{
		Id:            uuid.NewString(),
		Org:           orgID,
		Name:          "dlp",
		Priority:      0,
		InstalledBy:   installedBy,
		ConnectionIDs: make([]string, 0),
		Connections:   make([]pluginConfigSimpleName, 0),
	}
	reviewPlugin := plugin{
		Id:            uuid.NewString(),
		Org:           orgID,
		Name:          "review",
		Priority:      0,
		InstalledBy:   installedBy,
		ConnectionIDs: make([]string, 0),
		Connections:   make([]pluginConfigSimpleName, 0),
	}

	allPlugins, err := getPlugins(orgID)
	if err != nil {
		fmt.Printf("failed to get existing plugins, err=%v\n", err)
		os.Exit(-1)
	}

	for _, v := range allPlugins {
		p, err := getPlugin(v)
		if err != nil {
			fmt.Printf("failed to get plugin, err=%v\n", err)
			os.Exit(-1)
		}
		if p.Name == "audit" {
			auditPlugin = p
		}
		if p.Name == "review" {
			reviewPlugin = p
		}
		if p.Name == "dlp" {
			dlpPlugin = p
		}
	}

	for _, t := range targets {
		// audit
		auditPlugin.Connections = append(auditPlugin.Connections, pluginConfigSimpleName{
			ConnectionID: t.Id,
			Config:       nil,
		})
		auditConfig := pluginConfig{
			Id:           uuid.NewString(),
			ConnectionID: t.Id,
			Config:       nil,
		}
		pluginsConfig = append(pluginsConfig, auditConfig)
		auditPlugin.ConnectionIDs = append(auditPlugin.ConnectionIDs, auditConfig.Id)

		//dlp
		if t.Redact == "all" {
			dlpPlugin.Connections = append(dlpPlugin.Connections, pluginConfigSimpleName{
				ConnectionID: t.Id,
				Config:       dlpFields,
			})
			dlpConfig := pluginConfig{
				Id:           uuid.NewString(),
				ConnectionID: t.Id,
				Config:       dlpFields,
			}
			pluginsConfig = append(pluginsConfig, dlpConfig)
			dlpPlugin.ConnectionIDs = append(dlpPlugin.ConnectionIDs, dlpConfig.Id)
		}

		//review
		if t.ReviewType == "team" {
			reviewPlugin.Connections = append(reviewPlugin.Connections, pluginConfigSimpleName{
				ConnectionID: t.Id,
				Config:       t.Reviewers,
			})
			reviewConfig := pluginConfig{
				Id:           uuid.NewString(),
				ConnectionID: t.Id,
				Config:       t.Reviewers,
			}
			pluginsConfig = append(pluginsConfig, reviewConfig)
			reviewPlugin.ConnectionIDs = append(reviewPlugin.ConnectionIDs, reviewConfig.Id)
		}

		plugins = append(plugins, auditPlugin, dlpPlugin, reviewPlugin)
	}
	return plugins, pluginsConfig
}

func submitTx(items []any) error {
	payload, err := buildSubmitTxPayload(items)
	if err != nil {
		return err
	}

	requestURL := fmt.Sprintf(c.host, "_xtdb/submit-tx")
	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return errors.New("not 202")
	}

	fmt.Printf("Submitted %d items\n", len(items))
	return nil
}

func buildSubmitTxPayload(objs []any) ([]byte, error) {
	txOps := make([]any, 0)
	for _, obj := range objs {
		txOps = append(txOps, []any{"put", obj})
	}
	return json.Marshal(map[string]any{"tx-ops": txOps})
}

func getOrgID(org string) (string, error) {
	fmt.Println("Getting org...")
	ednQuery := []byte(`
		{:query 
		 {:find [?org]
			:in [name]
		  :where [[?org :org/name name]]}
		  :in-args ["` + org + `"]}`)

	result, err := getItems(ednQuery)
	if err != nil {
		return "", err
	}

	if len(result) == 0 {
		return "", errors.New("not found")
	}

	return result[0], nil
}

func getItems(ednQuery []byte) ([]string, error) {
	bodyReader := bytes.NewReader(ednQuery)

	requestURL := fmt.Sprintf(c.host, "_xtdb/query")
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

	var result [][]string
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}

	var innerResult []string
	for _, i := range result {
		for _, j := range i {
			innerResult = append(innerResult, j)
		}
	}

	fmt.Printf("Found %d items\n", len(innerResult))
	return innerResult, nil
}

func getAgents(orgID string) ([]string, error) {
	fmt.Println("Getting agents...")
	ednBody := []byte(fmt.Sprintf(`
		{:query 
		 {:find [?obj]
			:in [orgID]
		  :where [[?obj :agent/org orgID]]}
		  :in-args ["%s"]}`, orgID))

	return getItems(ednBody)
}

func getUsers(orgID string) ([]string, error) {
	fmt.Println("Getting users...")
	ednBody := []byte(fmt.Sprintf(`
		{:query 
		 {:find [?obj]
			:in [orgID]
		  :where [[?obj :user/org orgID]]}
		  :in-args ["%s"]}`, orgID))

	return getItems(ednBody)
}

var dlpFields = []string{"PHONE_NUMBER",
	"CREDIT_CARD_NUMBER",
	"CREDIT_CARD_TRACK_NUMBER",
	"EMAIL_ADDRESS",
	"IBAN_CODE",
	"HTTP_COOKIE",
	"IMEI_HARDWARE_ID",
	"IP_ADDRESS",
	"STORAGE_SIGNED_URL",
	"URL",
	"VEHICLE_IDENTIFICATION_NUMBER",
	"BRAZIL_CPF_NUMBER",
	"AMERICAN_BANKERS_CUSIP_ID",
	"FDA_CODE",
	"US_PASSPORT",
	"US_SOCIAL_SECURITY_NUMBER",
}

func getPlugins(orgID string) ([]string, error) {
	fmt.Println("Getting plugins...")
	ednBody := []byte(fmt.Sprintf(`
		{:query 
		 {:find [?obj]
			:in [orgID]
		  :where [[?obj :plugin/org orgID]]}
		  :in-args ["%s"]}`, orgID))

	return getItems(ednBody)
}

func getPlugin(id string) (plugin, error) {
	requestURL := fmt.Sprintf(c.host, "_xtdb/entity")
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return plugin{}, nil
	}

	q := req.URL.Query()
	q.Set("eid", id)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return plugin{}, err
	}

	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return plugin{}, err
	}

	var p plugin
	if err := json.Unmarshal(b, &p); err != nil {
		return plugin{}, err
	}

	return p, nil
}

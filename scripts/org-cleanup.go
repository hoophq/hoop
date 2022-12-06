package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type (
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
	var org string
	if len(os.Args) > 1 {
		org = os.Args[1]
	}

	if org == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Org name [required]: ")
		org, _ = reader.ReadString('\n')
		org = strings.Trim(org, " \n")
	}

	if org == "" {
		fmt.Println("Org not provided. Exiting...")
		os.Exit(1)
	}

	fmt.Printf("Cleaning up org [%s]...\n", org)

	if err := cleanupOrg(org); err != nil {
		fmt.Printf("Failed with error: %s\n", err.Error())
		os.Exit(1)
	}
}

func cleanupOrg(org string) error {
	orgID, err := getOrgID(org)
	if err != nil {
		return err
	}

	fmt.Printf("Found org ID [%s]\n", orgID)

	reviewIDs, err := getReviews(orgID)
	if err != nil {
		return err
	}

	connectionIDs, err := getConnections(orgID)
	if err != nil {
		return err
	}

	pluginIDs, err := getPlugins(orgID)
	if err != nil {
		return err
	}

	agentIDs, err := getAgents(orgID)
	if err != nil {
		return err
	}

	userIDs, err := getUsers(orgID)
	if err != nil {
		return err
	}

	deleteList := assembleIDs(
		reviewIDs,
		connectionIDs,
		pluginIDs,
		agentIDs,
		userIDs)

	if err := submitTx(deleteList); err != nil {
		return err
	}

	return nil
}

func getOrgID(org string) (string, error) {
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

func getReviews(orgID string) ([]string, error) {
	fmt.Println("Getting reviews...")
	ednBody := []byte(fmt.Sprintf(`
		{:query 
		 {:find [?obj]
			:in [orgID]
		  :where [[?obj :review/org orgID]]}
		  :in-args ["%s"]}`, orgID))

	return getItems(ednBody)
}

func getConnections(orgID string) ([]string, error) {
	fmt.Println("Getting connections...")
	ednBody := []byte(fmt.Sprintf(`
		{:query 
		 {:find [?obj]
			:in [orgID]
		  :where [[?obj :connection/org orgID]]}
		  :in-args ["%s"]}`, orgID))

	return getItems(ednBody)
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

func assembleIDs(items ...[]string) []string {
	var result []string

	for _, i := range items {
		result = append(result, i...)
	}

	return result
}

func submitTx(items []string) error {
	fmt.Printf("Deleting %d items\n", len(items))
	return nil
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

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
	host:   "http://127.0.0.1:8999/%s",
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
	orgIDs, err := getOrgID(org)
	if err != nil {
		return err
	}

	fmt.Printf("Found org ID [%s]\n", orgIDs)
	return nil
}

func getOrgID(org string) (string, error) {
	ednBody := []byte(`
		{:query 
		 {:find [(pull ?org [*])]
			:in [name]
		  :where [[?org :org/name name]]}
		  :in-args ["` + org + `"]}`)

	bodyReader := bytes.NewReader(ednBody)

	requestURL := fmt.Sprintf(c.host, "_xtdb/query")
	req, err := http.NewRequest(http.MethodPost, requestURL, bodyReader)
	if err != nil {
		return "", nil
	}

	req.Header.Set("Content-type", "application/edn")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result [][]map[string]string
	if err := json.Unmarshal(b, &result); err != nil {
		return "", nil
	}

	if len(result) == 0 {
		return "", errors.New("org not found")
	}

	orgList := result[0]
	if len(orgList) == 0 {
		return "", errors.New("org not found")
	}

	orgMap := orgList[0]
	orgID := orgMap["xt/id"]

	return orgID, nil
}

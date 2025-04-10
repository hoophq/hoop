package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

type apiResource struct {
	resourceType    string
	name            string
	method          string
	decodeTo        string
	suffixEndpoint  string
	queryAttributes url.Values
	conf            *clientconfig.Config

	resourceList   bool
	resourceGet    bool
	resourceDelete bool
	resourceCreate bool
	resourceUpdate bool
}

func parseResourceOrDie(args []string, method, outputFlag string) *apiResource {
	method = strings.ToUpper(method)
	conf := clientconfig.GetClientConfigOrDie()
	var resourceType, resourceName string
	switch len(args) {
	case 1:
		resourceType, resourceName, _ = strings.Cut(args[0], "/")
	case 2:
		resourceType = args[0]
		resourceName = args[1]
	}

	apir := &apiResource{
		resourceType:    resourceType,
		name:            resourceName,
		conf:            conf,
		method:          method,
		resourceGet:     true,
		resourceList:    true,
		queryAttributes: url.Values{},
	}

	switch apir.resourceType {
	case "agents", "agent":
		apir.resourceGet = false
		apir.resourceDelete = true
		apir.resourceCreate = true
		apir.suffixEndpoint = path.Join("/api/agents", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/agents"
		}
	case "conn", "connection", "connections":
		apir.resourceDelete = true
		apir.resourceCreate = true
		apir.resourceUpdate = true
		apir.suffixEndpoint = path.Join("/api/connections", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/connections"
		}
	case "orglicense":
		apir.resourceGet = true
		apir.resourceCreate = true
		apir.resourceUpdate = true
		apir.suffixEndpoint = "/api/orgs/license"
		if method == "POST" {
			apir.suffixEndpoint = "/api/orgs/license/sign"
		}
		apir.name = "_"
	case "orgkey", "orgkeys":
		apir.resourceDelete = true
		apir.resourceList = true
		apir.resourceCreate = true
		apir.suffixEndpoint = "/api/orgs/keys"
		defer func() {
			apir.name = ""
			if outputFlag == "" {
				apir.decodeTo = "object"
			}
		}()
		apir.name = "_"
	case "sessions":
		defer func() {
			if outputFlag == "" {
				apir.decodeTo = "object"
			}
		}()
		apir.resourceList = true
		apir.suffixEndpoint = path.Join("/api/sessions", apir.name)
	case "users":
		apir.resourceUpdate = true
		apir.resourceCreate = true
		apir.resourceDelete = true
		apir.suffixEndpoint = path.Join("/api/users", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/users"
		}
	case "userinfo":
		defer func() {
			if outputFlag == "" {
				apir.decodeTo = "object"
			}
		}()
		apir.resourceList = true
		apir.resourceGet = false
		apir.suffixEndpoint = "/api/userinfo"
	case "serviceaccount", "serviceaccounts", "sa":
		apir.resourceList = true
		apir.resourceUpdate = true
		apir.resourceCreate = true
		apir.suffixEndpoint = path.Join("/api/serviceaccounts", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/serviceaccounts"
		}
	case "review", "reviews":
		apir.suffixEndpoint = path.Join("/api/reviews", apir.name)
	case "plugin", "plugins":
		apir.resourceCreate = true
		apir.resourceUpdate = true
		apir.suffixEndpoint = path.Join("/api/plugins", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/plugins"
		}
	case "policies", "policy":
		apir.resourceList = true
		apir.resourceDelete = true
		apir.resourceGet = false
		apir.suffixEndpoint = "/api/policies"
		if method == "DELETE" {
			apir.suffixEndpoint = path.Join("/api/policies", apir.name)
		}
	case "datamasking", "accesscontrol":
		apir.resourceCreate = true
		apir.resourceUpdate = true
		apir.resourceGet = false
		apir.resourceList = false
		apir.suffixEndpoint = path.Join("/api/policies", apir.resourceType, apir.name)
		if method == "POST" {
			apir.suffixEndpoint = path.Join("/api/policies", apir.resourceType)
		}
	case "runbooks":
		// force to decode as object
		apir.resourceGet = true
		apir.name = "noop"

		apir.resourceList = false
		apir.suffixEndpoint = "/api/plugins/runbooks/templates"
	case "svixendpoint", "svixendpoints", "svixep":
		defer func() {
			if outputFlag == "" {
				apir.decodeTo = "object"
			}
		}()
		apir.resourceGet = false
		apir.resourceDelete = true
		apir.resourceCreate = true
		apir.resourceUpdate = true
		apir.suffixEndpoint = path.Join("/api/webhooks/endpoints", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/webhooks/endpoints"
		}
	case "svixmessage", "svixmessages", "svixmsg":
		defer func() {
			if outputFlag == "" {
				apir.decodeTo = "object"
			}
		}()
		apir.resourceCreate = true
		apir.suffixEndpoint = path.Join("/api/webhooks/messages", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/webhooks/messages"
		}
	case "svixeventtype", "svixeventtypes", "svixet":
		defer func() {
			if outputFlag == "" {
				apir.decodeTo = "object"
			}
		}()
		apir.resourceDelete = true
		apir.resourceCreate = true
		apir.resourceUpdate = true
		apir.suffixEndpoint = path.Join("/api/webhooks/eventtypes", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/webhooks/eventtypes"
		}
	default:
		styles.PrintErrorAndExit("resource type %q not supported", apir.resourceType)
	}

	// resource contraints
	switch method {
	case "GET":
		if !apir.resourceGet && !apir.resourceList {
			styles.PrintErrorAndExit("method GET not implemented for resource %q ", apir.resourceType)
		}

		if !apir.resourceGet && apir.resourceList && apir.name != "" {
			styles.PrintErrorAndExit("only list is available for resource %q", apir.resourceType)
		}

		if !apir.resourceList && apir.resourceGet && apir.name == "" {
			styles.PrintErrorAndExit("missing resource name")
		}
		apir.decodeTo = "list"
		if apir.name != "" {
			apir.decodeTo = "object"
		}
	case "DELETE":
		if !apir.resourceDelete {
			styles.PrintErrorAndExit("method %v not implemented for resource %q", method, apir.resourceType)
		}
		if apir.name == "" {
			styles.PrintErrorAndExit("missing resource name")
		}
		apir.decodeTo = "object"
	case "PUT":
		if !apir.resourceUpdate {
			styles.PrintErrorAndExit("method %v not implemented for resource %q", method, apir.resourceType)
		}
		if apir.name == "" {
			styles.PrintErrorAndExit("missing resource name")
		}
	case "POST":
		if !apir.resourceCreate {
			styles.PrintErrorAndExit("method %v not implemented for resource %q", method, apir.resourceType)
		}
	default:
		styles.PrintErrorAndExit("http method not implemented %v", method)
	}

	// override if flag is provided
	if outputFlag == "json" {
		apir.decodeTo = "raw"
	}

	log.Debugf("decode=%v, method=%v, create=%v, list=%v, get=%v, delete=%v, path=%v",
		apir.decodeTo, apir.method, apir.resourceCreate, apir.resourceList,
		apir.resourceGet, apir.resourceDelete, apir.suffixEndpoint)
	return apir
}

func (r *apiResource) Endpoint() (string, error) {
	u, err := url.Parse(r.conf.ApiURL)
	if err != nil {
		return "", err
	}

	if len(r.queryAttributes) > 0 {
		u.RawQuery = r.queryAttributes.Encode()
	}
	return u.JoinPath(r.suffixEndpoint).String(), nil
}

func httpRequest(apir *apiResource) (any, http.Header, error) {
	url, err := apir.Endpoint()
	if err != nil {
		return nil, nil, err
	}
	log.Debugf("performing http request at GET %v", url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating http request, err=%v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", apir.conf.Token))
	if apir.conf.IsApiKey() {
		req.Header.Set("Api-Key", apir.conf.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%s", hoopVersionStr))
	resp, err := httpclient.NewHttpClient(apir.conf.TlsCA()).Do(req)
	if err != nil {
		return nil, nil, err
	}
	log.Debugf("http response %v", resp.StatusCode)
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 && resp.StatusCode != 204 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, resp.Header, fmt.Errorf("failed performing request, status=%v, body=%v",
			resp.StatusCode, string(respBody))
	}
	switch apir.decodeTo {
	case "list":
		var mapList []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&mapList); err != nil {
			return nil, resp.Header, fmt.Errorf("failed decoding response, codec=%v, status=%v, err=%v",
				apir.decodeTo, resp.StatusCode, err)
		}
		return mapList, resp.Header, nil
	case "object":
		var respMap map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&respMap); err != nil {
			return nil, resp.Header, fmt.Errorf("failed decoding response, codec=%v, status=%v, err=%v",
				apir.decodeTo, resp.StatusCode, err)
		}
		return respMap, resp.Header, nil
	default:
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, resp.Header, fmt.Errorf("failed reading content body, status=%v, err=%v", resp.StatusCode, err)
		}
		return respBody, resp.Header, nil
	}

}

func httpDeleteRequest(apir *apiResource) error {
	url, err := apir.Endpoint()
	if err != nil {
		return err
	}
	log.Debugf("performing http request at DELETE %v", url)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed creating http request, err=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", apir.conf.Token))
	if apir.conf.IsApiKey() {
		req.Header.Set("Api-Key", apir.conf.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%s", hoopVersionStr))
	resp, err := httpclient.NewHttpClient(apir.conf.TlsCA()).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	log.Debugf("http response %v", resp.StatusCode)
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("failed removing resource. status=%v, body=%v", resp.StatusCode, string(respBody))
}

func httpBodyRequest(apir *apiResource, method string, bodyMap map[string]any) (any, error) {
	url, err := apir.Endpoint()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("failed encoding body, err=%v", err)
	}
	log.Debugf("performing http request at %v %v", method, url)
	log.Debugf("payload=%v", string(body))
	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed creating http request, err=%v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", apir.conf.Token))
	if apir.conf.IsApiKey() {
		req.Header.Set("Api-Key", apir.conf.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%s", hoopVersionStr))
	resp, err := httpclient.NewHttpClient(apir.conf.TlsCA()).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	log.Debugf("http response %v", resp.StatusCode)
	if resp.StatusCode != 200 && resp.StatusCode != 201 && resp.StatusCode != 204 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed performing request, status=%v, body=%v",
			resp.StatusCode, string(respBody))
	}
	if resp.StatusCode == 204 {
		return nil, nil
	}
	if apir.decodeTo == "raw" {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed reading content body, status=%v, err=%v", resp.StatusCode, err)
		}
		return respBody, nil
	}
	var respMap map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&respMap); err != nil {
		return nil, fmt.Errorf("failed decoding response, codec=%v, status=%v, err=%v",
			apir.decodeTo, resp.StatusCode, err)
	}
	return respMap, nil
}

func NormalizeResourceName(name string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	name, _, _ = transform.String(t, name)
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

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

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/appruntime"
	"github.com/runopsio/hoop/common/log"
)

var isApiV2 = appruntime.IsApiV2()

type apiResource struct {
	resourceType   string
	name           string
	method         string
	decodeTo       string
	suffixEndpoint string
	conf           *clientconfig.Config

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
		resourceType: resourceType,
		name:         resourceName,
		conf:         conf,
		method:       method,
		resourceGet:  true,
		resourceList: true,
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
	case "sessions":
		apir.resourceList = false
		apir.suffixEndpoint = path.Join("/api/sessions", apir.name)
	case "users":
		apir.resourceUpdate = true
		apir.resourceCreate = true
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
	// DEPRECATED
	case "clientkeys":
		apir.resourceCreate = true
		apir.resourceUpdate = true
		apir.suffixEndpoint = path.Join("/api/clientkeys", apir.name)
		if method == "POST" {
			apir.suffixEndpoint = "/api/clientkeys"
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

	log.Debugf("decode=%v, method=%v, create=%v, list=%v, get=%v, delete=%v, apiv2=%v, path=%v",
		apir.decodeTo, apir.method, apir.resourceCreate, apir.resourceList,
		apir.resourceGet, apir.resourceDelete, isApiV2, apir.suffixEndpoint)
	return apir
}

func (r *apiResource) Endpoint() (string, error) {
	return url.JoinPath(r.conf.ApiURL, r.suffixEndpoint)
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
	if isApiV2 {
		req.Header.Set("x-backend-api", "express")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", apir.conf.Token))
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%s", hoopVersionStr))
	resp, err := http.DefaultClient.Do(req)
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
	if isApiV2 {
		req.Header.Set("x-backend-api", "express")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", apir.conf.Token))
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%s", hoopVersionStr))
	resp, err := http.DefaultClient.Do(req)
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

	if isApiV2 {
		req.Header.Set("x-backend-api", "express")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", apir.conf.Token))
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%s", hoopVersionStr))
	resp, err := http.DefaultClient.Do(req)
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

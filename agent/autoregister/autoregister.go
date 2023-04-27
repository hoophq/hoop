package autoregister

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/runopsio/hoop/common/log"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/runtime"
	"github.com/runopsio/hoop/common/version"
	"olympos.io/encoding/edn"
)

// Run auto register an agent if it's deployed in the same network of xtdb
// intended to be used to perform administrative tasks in the system
func Run() (string, error) {
	if os.Getenv("AUTO_REGISTER") == "" {
		return "", nil
	}
	agentID := "agent/default"
	agentToken, err := fetchAgentDefaultToken(agentID)
	if err != nil {
		return "", err
	}

	if agentToken != "" {
		log.Printf("auto registering - found the default agent")
		return agentToken, nil
	}

	orgID, err := fetchDefaultOrgID()
	if err != nil {
		return "", err
	}
	osmap := runtime.OS()
	vinfo := version.Get()
	agentToken = fmt.Sprintf("x-agt-%s", uuid.NewString())
	ednquery := fmt.Sprintf(`{:tx-ops
		[[:xtdb.api/put {
		:xt/id %q,
		:agent/token %q,
		:agent/org %q,
		:agent/name "default",
		:agent/hostname %q,
		:agent/machine-id %q,
		:agent/kernel-version %q,
		:agent/version %q,
		:agent/go-version %q,
		:agent/compiler %q,
		:agent/platform %q,
		:agent/created-by "agent-auto-register"}]]}`,
		agentID, agentToken, orgID, osmap["hostname"], osmap["machine_id"],
		osmap["kernel_version"], vinfo.Version, vinfo.GoVersion, vinfo.Compiler, vinfo.Platform)

	_, err = xtdbHttpRequest("http://127.0.0.1:3000/_xtdb/submit-tx", ednquery)
	if err != nil {
		return "", fmt.Errorf("failed auto registering. %v", err)
	}
	log.Info("autoregister - created default agent")
	return agentToken, nil
}

func fetchDefaultOrgID() (string, error) {
	ednquery := fmt.Sprintf(`{:query
		{:find [id]
		:in [orgname]
		:where [[?o :org/name orgname]
				[?o :xt/id id]]}
		:in-args [%q]}`, pb.DefaultOrgName)
	httpResponse, err := xtdbHttpRequest("http://127.0.0.1:3000/_xtdb/query", ednquery)
	if err != nil {
		return "", fmt.Errorf("failed auto registering. %v", err)
	}
	var ednResp [][]string
	if err := edn.Unmarshal(httpResponse, &ednResp); err != nil {
		return "", fmt.Errorf("failed auto registering, error decoding edn response, err=%v", err)
	}
	if len(ednResp) > 0 {
		return ednResp[0][0], nil
	}
	return "", fmt.Errorf("failed auto registering - organization %q not found", pb.DefaultOrgName)
}

func fetchAgentDefaultToken(agentID string) (string, error) {
	ednquery := fmt.Sprintf(`{:query
		{:find [agent-token]
		:in [orgname, agentid]
		:where [[?o :org/name orgname]
				[?a :agent/token agent-token]
				[?a :xt/id agentid]]}
		:in-args [%q %q]}`, pb.DefaultOrgName, agentID)
	httpResponse, err := xtdbHttpRequest("http://127.0.0.1:3000/_xtdb/query", ednquery)
	if err != nil {
		return "", fmt.Errorf("failed auto registering. %v", err)
	}
	var ednResp [][]string
	if err := edn.Unmarshal(httpResponse, &ednResp); err != nil {
		return "", fmt.Errorf("failed auto registering, error decoding edn response, err=%v", err)
	}
	if len(ednResp) > 0 {
		return ednResp[0][0], nil
	}
	return "", nil
}

func xtdbHttpRequest(apiURL, ednQuery string) ([]byte, error) {
	resp, err := http.DefaultClient.Post(
		apiURL, "application/edn", bytes.NewBuffer([]byte(ednQuery)),
	)
	var httpResponse []byte
	var statusCode int
	if resp != nil {
		defer resp.Body.Close()
		httpResponse, _ = io.ReadAll(resp.Body)
		statusCode = resp.StatusCode
	}
	if err != nil || statusCode > 299 {
		return nil, fmt.Errorf("code=%v, httpresponse=%v, err=%v",
			statusCode, string(httpResponse), err)
	}
	return httpResponse, nil
}

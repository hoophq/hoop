package autoregister

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/appruntime"
	"github.com/runopsio/hoop/common/dsnkeys"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"olympos.io/encoding/edn"
)

const agentName = "default"

// Run auto register an agent if it's deployed in the same network of xtdb
// intended to be used to perform administrative tasks in the system
func Run() (string, error) {
	if os.Getenv("AUTO_REGISTER") == "" {
		return "", nil
	}
	agentID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(`agent/default`)).String()
	orgID, err := fetchDefaultOrgID()
	if err != nil {
		return "", err
	}
	osmap := appruntime.OS()
	vinfo := version.Get()
	skey, skeyHash, err := dsnkeys.GenerateSecureRandomKey()
	if err != nil {
		return "", err
	}
	dsn, err := dsnkeys.NewString("grpc://127.0.0.1:8010", agentName, skey, pb.AgentModeStandardType)
	if err != nil {
		return "", err
	}
	// agentToken = fmt.Sprintf("x-agt-%s", uuid.NewString())
	ednquery := fmt.Sprintf(`{:tx-ops
		[[:xtdb.api/put {
		:xt/id %q,
		:agent/token %q,
		:agent/org %q,
		:agent/name %q,
		:agent/mode %q,
		:agent/hostname %q,
		:agent/machine-id %q,
		:agent/kernel-version %q,
		:agent/version %q,
		:agent/go-version %q,
		:agent/compiler %q,
		:agent/platform %q,
		:agent/created-by "agent-auto-register"}]]}`,
		agentID, skeyHash, orgID, agentName, pb.AgentModeStandardType,
		osmap["hostname"], osmap["machine_id"], osmap["kernel_version"], vinfo.Version,
		vinfo.GoVersion, vinfo.Compiler, vinfo.Platform)
	_, err = xtdbHttpRequest("http://127.0.0.1:3001/_xtdb/submit-tx", ednquery)
	if err != nil {
		return "", fmt.Errorf("failed auto registering. %v", err)
	}
	_, _ = http.Get("http://127.0.0.1:3001/_xtdb/sync?timeout=5000")
	log.Info("sync default agent with success")
	return dsn, nil
}

func fetchDefaultOrgID() (string, error) {
	ednquery := fmt.Sprintf(`{:query
		{:find [id]
		:in [orgname]
		:where [[?o :org/name orgname]
				[?o :xt/id id]]}
		:in-args [%q]}`, pb.DefaultOrgName)
	httpResponse, err := xtdbHttpRequest("http://127.0.0.1:3001/_xtdb/query", ednquery)
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

package agentcontroller

import (
	"fmt"
	"hash/crc32"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/agentcontroller"
	"github.com/runopsio/hoop/common/dsnkeys"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgagents "github.com/runopsio/hoop/gateway/pgrest/agents"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/user"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	defaultPrefixAgentName = "demo"
	defaultTickTime        = time.Minute * 5
)

var (
	tickerCh chan bool
	jobMutex sync.RWMutex
)

func Run(gatewayApiURL string) error {
	if !user.IsOrgMultiTenant() {
		return nil
	}
	client, err := NewApiClient()
	if err != nil {
		return err
	}
	tickerCh = make(chan bool)
	go func() {
		for {
			time.Sleep(defaultTickTime)
			Sync()
		}
	}()
	log.Info("stating sync deployments")
	go func() {
		for range tickerCh {
			var conciliationItems []*agentcontroller.AgentRequest
			deploymentItems, err := listAgents(client)
			if err != nil {
				log.Warnf("failed listing deployments, reason=%v", err)
				continue
			}
			orgList, err := pgusers.New().FetchAllOrgs()
			if err != nil {
				log.Warnf("failed listing organizations, reason=%v", err)
				continue
			}
			for _, org := range orgList {
				// it will ensure that deployments are unique per organization
				deployName := newHashName(org.ID, normalizeOrgName(org.Name))
				if shouldConciliate(deploymentItems, deployName) {
					conciliationItems = append(conciliationItems, &agentcontroller.AgentRequest{
						ID:       org.ID,
						Name:     deployName,
						DSNKey:   "",
						ImageRef: "",
					})
				}
			}
			var errReport []string
			if len(conciliationItems) > 0 {
				errReport = conciliateDeployments(gatewayApiURL, conciliationItems, client)
			}
			successSyncItems := len(conciliationItems) - len(errReport)
			log.Infof("finish deployment concialtion, sync=%v, success=%v/%v",
				len(conciliationItems) > 0, len(conciliationItems), successSyncItems)
			for _, err := range errReport {
				log.Warn(err)
			}
		}
	}()
	return nil
}

func Sync() {
	jobMutex.Lock()
	defer jobMutex.Unlock()
	timeout := time.NewTimer(time.Second * 3)
	defer timeout.Stop()
	select {
	case tickerCh <- true:
	case <-timeout.C:
		log.Warnf("timeout (3s) sending sync deployment")
	}
}

func conciliateDeployments(gatewayApiURL string, items []*agentcontroller.AgentRequest, client *apiClient) (errReport []string) {
	agentcli := pgagents.New()
	for _, req := range items {
		dsnKey, secretKeyHash, err := generateDsnKey(gatewayApiURL, req.Name)
		if err != nil {
			errReport = append(errReport, fmt.Sprintf("failed generating secret key for agent demo %s/%s, err=%v", req.ID, req.Name, err))
			continue
		}
		req.DSNKey = dsnKey
		agent, err := agentcli.FetchOneByNameOrID(pgrest.NewOrgContext(req.ID), req.Name)
		if err != nil {
			errReport = append(errReport, fmt.Sprintf("failed obtaining agent demo %s/%s, err=%v", req.ID, req.Name, err))
			continue
		}
		if agent == nil {
			agent = &pgrest.Agent{
				ID:       uuid.NewString(),
				OrgID:    req.ID,
				Name:     req.Name,
				Mode:     proto.AgentModeStandardType,
				Token:    "",
				Status:   "DISCONNECTED",
				Metadata: map[string]string{},
			}
		}
		err = agentcli.Upsert(&pgrest.Agent{
			ID:     agent.ID,
			OrgID:  agent.OrgID,
			Token:  secretKeyHash,
			Name:   agent.Name,
			Mode:   agent.Mode,
			Status: string(agent.Status),
			Metadata: map[string]string{
				"hostname":       agent.GetMeta("hostname"),
				"platform":       agent.GetMeta("platform"),
				"goversion":      agent.GetMeta("goversion"),
				"version":        agent.GetMeta("version"),
				"kernel_version": agent.GetMeta("kernel_version"),
				"compiler":       agent.GetMeta("compiler"),
				"machine_id":     agent.GetMeta("machine_id"),
			}})
		if err != nil {
			errReport = append(errReport, fmt.Sprintf("failed updating agent demo %s/%s, err=%v", req.ID, req.Name, err))
		}

		if _, err := client.Update(req); err != nil {
			errReport = append(errReport, fmt.Sprintf("failed deploying agent demo %s/%s, err=%v", req.ID, req.Name, err))
		}
	}
	return
}

func shouldConciliate(items map[string]*agentcontroller.Deployment, itemKey string) bool {
	dp, found := items[itemKey]
	if !found {
		return true
	}
	if dp.Status.Replicas != 1 {
		return true
	}
	return false
}

func listAgents(client *apiClient) (map[string]*agentcontroller.Deployment, error) {
	deploymentList, err := client.List()
	if err != nil {
		return nil, fmt.Errorf("failed listing deployments, reason=%v", err)
	}
	items := map[string]*agentcontroller.Deployment{}
	for _, dp := range deploymentList {
		items[dp.Name] = &dp
	}
	return items, nil
}

func generateDsnKey(gatewayApiURL, agentName string) (dsnKey, secretKeyHash string, err error) {
	var secretKey string
	secretKey, secretKeyHash, err = dsnkeys.GenerateSecureRandomKey()
	if err != nil {
		return
	}
	dsnKey, err = dsnkeys.NewString(gatewayApiURL, agentName, secretKey, proto.AgentModeStandardType)
	return dsnKey, secretKeyHash, err
}

var reSpecialChars, _ = regexp.Compile(`[^\w]`)

func normalizeOrgName(orgName string) string {
	t := transform.Chain(norm.NFD, transform.RemoveFunc(func(r rune) bool {
		return unicode.Is(unicode.Mn, r)
	}), norm.NFC)
	orgName = strings.ReplaceAll(orgName, " ", "")
	orgName = strings.ToLower(orgName)
	// TODO: return error here
	orgName, _, _ = transform.String(t, orgName)
	if len(orgName) > 36 {
		orgName = orgName[:36]
	}
	orgName = reSpecialChars.ReplaceAllString(orgName, "")
	return fmt.Sprintf("%s-%s", defaultPrefixAgentName, orgName)
}

// newHashName generates a deterministic name based on the prefix of the name
// taking the crc32 checksum of the id (e.g.: name-crc32has)
func newHashName(id, name string) string {
	t := crc32.MakeTable(crc32.IEEE)
	return fmt.Sprintf("%s-%08x", name, crc32.Checksum([]byte(id), t))
}

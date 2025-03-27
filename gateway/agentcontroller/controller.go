package agentcontroller

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/agentcontroller"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgagents "github.com/hoophq/hoop/gateway/pgrest/agents"
)

const defaultTickTime = time.Minute * 5

var (
	tickerCh chan bool
	jobMutex sync.RWMutex
)

func Run(gatewayGrpcURL string) error {
	if !appconfig.Get().OrgMultitenant() {
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
			orgList, err := models.ListAllOrganizations()
			if err != nil {
				log.Warnf("failed listing organizations, reason=%v", err)
				continue
			}
			for _, org := range orgList {
				t30days := time.Now().UTC().AddDate(0, 0, 30)
				if org.CreatedAt.After(t30days) {
					log.Infof("removing agent deployment %v", org.ID)
					if err := client.Remove(org.ID, "noop"); err != nil {
						log.Warnf("failed removing agent deployment, reason=%v", err)
					}
					continue
				}
				// it will ensure that deployments are unique per organization
				deployName := org.ID
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
				errReport = conciliateDeployments(gatewayGrpcURL, conciliationItems, client)
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

func conciliateDeployments(gatewayGrpcURL string, items []*agentcontroller.AgentRequest, client *apiClient) (errReport []string) {
	agentcli := pgagents.New()
	for _, req := range items {
		dsnKey, secretKeyHash, err := generateDsnKey(gatewayGrpcURL, req.Name)
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
				KeyHash:  "",
				Status:   pgrest.AgentStatusDisconnected,
				Metadata: map[string]string{},
			}
		}
		err = agentcli.Upsert(&pgrest.Agent{
			ID:      agent.ID,
			OrgID:   agent.OrgID,
			KeyHash: secretKeyHash,
			Name:    agent.Name,
			Mode:    agent.Mode,
			Status:  string(agent.Status),
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

func generateDsnKey(gatewayGrpcURL, agentName string) (dsnKey, secretKeyHash string, err error) {
	var secretKey string
	secretKey, secretKeyHash, err = dsnkeys.GenerateSecureRandomKey()
	if err != nil {
		return
	}
	dsnKey, err = dsnkeys.NewString(gatewayGrpcURL, agentName, secretKey, proto.AgentModeStandardType)
	return dsnKey, secretKeyHash, err
}

package agentcontroller

import (
	"fmt"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/agentcontroller"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
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
			removedDeployments := 0
			for _, org := range orgList {
				createdAtPlus45Days := org.CreatedAt.AddDate(0, 0, 45)
				if time.Now().UTC().After(createdAtPlus45Days) {
					removedDeployments++
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
			log.Infof("finish deployment concialtion, sync=%v, removed=%v, success=%v/%v",
				len(conciliationItems) > 0, removedDeployments, len(conciliationItems), successSyncItems)
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
	for _, req := range items {
		dsnKey, secretKeyHash, err := generateDsnKey(gatewayGrpcURL, req.Name)
		if err != nil {
			errReport = append(errReport, fmt.Sprintf("failed generating secret key for agent demo %s/%s, err=%v", req.ID, req.Name, err))
			continue
		}
		req.DSNKey = dsnKey
		err = models.CreateAgent(req.ID, req.Name, proto.AgentModeStandardType, secretKeyHash)
		if err == models.ErrAlreadyExists {
			err = models.RotateAgentSecretKey(req.ID, req.Name, secretKeyHash)
		}
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

package slack

import (
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/slack"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var ErrMissingRequiredCredentials = fmt.Errorf("missing required credentials for slack plugin")

const (
	PluginConfigEnvVarsParam = "plugin_config"
	slackMaxButtons          = 20
)

type (
	slackPlugin struct {
		TransportReleaseConnection reviewapi.TransportReleaseConnectionFunc
		apiURL                     string
	}
)

var instances map[string]*slack.SlackService
var mu sync.RWMutex

func getSlackServiceInstance(orgID string) *slack.SlackService {
	mu.Lock()
	defer mu.Unlock()
	return instances[orgID]
}

func removeSlackServiceInstance(orgID string) {
	mu.Lock()
	defer mu.Unlock()
	delete(instances, orgID)
}

func addSlackServiceInstance(orgID string, slackSvc *slack.SlackService) {
	mu.Lock()
	defer mu.Unlock()
	instances[orgID] = slackSvc
}

func New(releaseConnFn reviewapi.TransportReleaseConnectionFunc) *slackPlugin {
	instances = map[string]*slack.SlackService{}
	mu = sync.RWMutex{}
	return &slackPlugin{
		TransportReleaseConnection: releaseConnFn,
		apiURL:                     appconfig.Get().ApiURL(),
	}
}

func (p *slackPlugin) Name() string { return plugintypes.PluginSlackName }

func (p *slackPlugin) startSlackServiceInstance(orgID string, slackConfig *slackConfig) error {
	log.Infof("starting slack service instance for org %v", orgID)
	ss, err := slack.New(
		slackConfig.slackBotToken,
		slackConfig.slackAppToken,
		slackConfig.slackChannel,
		orgID,
		p.apiURL,
	)
	if err != nil {
		return fmt.Errorf("failed starting slack service, err=%v", err)
	}
	addSlackServiceInstance(orgID, ss)
	reviewRespCh := make(chan *slack.MessageReviewResponse)
	go func() {
		defer close(reviewRespCh)
		if err := ss.ProcessEvents(reviewRespCh); err != nil {
			log.Errorf("failed processing slack events for org %v, reason=%v", orgID, err)
			return
		}
		log.Infof("done processing events for org %v", orgID)
		ss.Close()
		removeSlackServiceInstance(orgID)
	}()

	// response channel
	go func() {
		for resp := range reviewRespCh {
			p.processEventResponse(&event{ss, resp, orgID})
		}
		log.Infof("close response channel for org %v", orgID)
	}()
	return nil
}

func (p *slackPlugin) OnStartup(_ plugintypes.Context) error {
	orgList, err := models.ListAllOrganizations()
	if err != nil {
		return fmt.Errorf("failed listing organizations: %v", err)
	}

	for _, org := range orgList {
		pl, err := models.GetPluginByName(org.ID, plugintypes.PluginSlackName)
		if err != nil && err != models.ErrNotFound {
			log.Errorf("failed retrieving plugin entity %v", err)
			continue
		}
		if pl == nil || len(pl.EnvVars) == 0 {
			continue
		}
		if pl.OrgID == "" {
			log.Errorf("inconsistent state (org) for plugin slack")
			continue
		}
		slackConfig, err := parseSlackConfig(pl.EnvVars)
		if err != nil {
			log.Errorf("failed parsing slack config for org %v, err=%v", pl.OrgID, err)
			continue
		}
		if err := p.startSlackServiceInstance(pl.OrgID, slackConfig); err != nil {
			log.Errorf("failed starting slack service for org %v, err=%v", pl.OrgID, err)
			continue
		}
	}
	return nil
}

func (p *slackPlugin) OnUpdate(oldState, newState plugintypes.PluginResource) error {
	slackInstance := getSlackServiceInstance(newState.GetOrgID())
	if slackInstance == nil {
		slackInstance = &slack.SlackService{}
	}
	switch {
	// when it creates the plugin for the first time
	// it should only start it, if the client has sent a valid slack configuration
	case oldState == nil:
		if newSlackConfig, _ := parseSlackConfig(newState.GetEnvVars()); newSlackConfig != nil {
			slackInstance.Close()
			return p.startSlackServiceInstance(newState.GetOrgID(), newSlackConfig)
		}
	// when previous configuration doesn't exists
	case len(oldState.GetEnvVars()) == 0:
		newSlackConfig, err := parseSlackConfig(newState.GetEnvVars())
		if err != nil {
			return err
		}
		return p.startSlackServiceInstance(newState.GetOrgID(), newSlackConfig)
	// when slack configuration changes
	default:
		if oldSlackConfig, _ := parseSlackConfig(oldState.GetEnvVars()); oldSlackConfig != nil {
			newSlackConfig, err := parseSlackConfig(newState.GetEnvVars())
			switch err {
			case ErrMissingRequiredCredentials:
				if slackInstance != nil {
					log.Warnf("configuration has changed to empty credentials, stopping slack instance %v", newState.GetOrgID())
					slackInstance.Close()
				}
				return nil
			case nil:
			default:
				return err

			}
			if oldSlackConfig.slackAppToken != newSlackConfig.slackAppToken ||
				oldSlackConfig.slackBotToken != newSlackConfig.slackBotToken {
				log.Warnf("configuration has changed, (re)starting slack instance %v", newState.GetOrgID())
				if slackInstance != nil {
					slackInstance.Close()
				}
				removeSlackServiceInstance(newState.GetOrgID())
				err := p.startSlackServiceInstance(newState.GetOrgID(), newSlackConfig)
				if err == nil {
					return nil
				}
				// rollback to previous configuration
				log.Warnf("previous configuration failed to start, (re)starting old slack instance %v", oldState.GetOrgID())
				if err := p.startSlackServiceInstance(oldState.GetOrgID(), oldSlackConfig); err != nil {
					log.Warnf("failed to rollback the initialization of slack %v, reason=%v", oldState.GetOrgID(), err)
				}
				return err
			}
		}
	}
	return nil
}

// SendApprovedMessage sends a message informing the session is ready
func SendApprovedMessage(orgID, slackID, sid, apiURL string) {
	if slacksvc := getSlackServiceInstance(orgID); slacksvc != nil {
		msg := fmt.Sprintf("Your session is ready.\nFollow this link to see the details: %s/sessions/%s",
			apiURL, sid)
		_ = slacksvc.PostMessage(slackID, msg)
	}
}

func (p *slackPlugin) OnConnect(pctx plugintypes.Context) error { return nil }
func (p *slackPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pkt.Type != pbagent.SessionOpen {
		return nil, nil
	}
	slackSvc := getSlackServiceInstance(pctx.OrgID)
	log.With("sid", pctx.SID).Infof("executing slack on-receive, hasinstance=%v", slackSvc != nil)
	if slackSvc == nil {
		return nil, nil
	}

	sreq := &slack.MessageReviewRequest{
		Name:           pctx.UserName,
		Email:          pctx.UserEmail,
		Connection:     pctx.ConnectionName,
		ConnectionType: pctx.ConnectionType,
		SessionID:      pctx.SID,
		UserGroups:     pctx.UserGroups,
		SlackChannels:  pctx.PluginConnectionConfig,
	}

	rev, err := models.GetReviewByIdOrSid(pctx.OrgID, pctx.SID)
	if err != nil && err != models.ErrNotFound {
		return nil, plugintypes.InternalErr("internal error, failed fetching review", err)
	}
	if rev != nil {
		if rev.Status != models.ReviewStatusPending {
			return nil, nil
		}
		reviewInput, err := rev.GetBlobInput()
		if err != nil {
			return nil, plugintypes.InternalErr("internal error, failed fetching review input", err)
		}
		sreq.ID = rev.ID
		sreq.WebappURL = fmt.Sprintf("%s/reviews/%s", p.apiURL, rev.ID)
		sreq.ApprovalGroups = parseGroups(rev.ReviewGroups)
		if rev.AccessDurationSec > 0 {
			ad := time.Duration(rev.AccessDurationSec) * time.Second
			sreq.SessionTime = &ad
		}
		sreq.Script = reviewInput
	}

	if sreq.WebappURL == "" || len(sreq.ApprovalGroups) == 0 || len(sreq.ApprovalGroups) >= slackMaxButtons {
		log.With("sid", pctx.SID).Infof("no review message to process, has-webapp-url=%v, approval-groups=%v/%v",
			sreq.WebappURL != "", len(sreq.ApprovalGroups), slackMaxButtons)
		return nil, nil
	}
	log.With("sid", pctx.SID).Infof("sending slack review message, conn=%v, jit=%v", sreq.Connection, sreq.SessionTime != nil)
	result := slackSvc.SendMessageReview(sreq)
	log.With("sid", pctx.SID).Infof("review slack message sent, %v", result)
	return nil, nil
}

func (p *slackPlugin) OnDisconnect(_ plugintypes.Context, _ error) error { return nil }
func (p *slackPlugin) OnShutdown()                                       {}

type slackConfig struct {
	slackBotToken string
	slackAppToken string
	slackChannel  string
}

func parseSlackConfig(envVars map[string]string) (*slackConfig, error) {
	if len(envVars) == 0 {
		return nil, ErrMissingRequiredCredentials
	}
	slackBotToken, _ := base64.StdEncoding.DecodeString(envVars["SLACK_BOT_TOKEN"])
	slackAppToken, _ := base64.StdEncoding.DecodeString(envVars["SLACK_APP_TOKEN"])
	slackChannel, _ := base64.StdEncoding.DecodeString(envVars["SLACK_CHANNEL"])
	sc := slackConfig{
		slackBotToken: string(slackBotToken),
		slackAppToken: string(slackAppToken),
		slackChannel:  string(slackChannel),
	}
	if sc.slackBotToken == "" || sc.slackAppToken == "" {
		return nil, fmt.Errorf("missing required slack credentials")
	}
	return &sc, nil
}

func parseGroups(reviewGroups []models.ReviewGroups) []string {
	groups := make([]string, 0)
	for _, g := range reviewGroups {
		groups = append(groups, g.GroupName)
	}
	return groups
}

package slack

import (
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	"github.com/hoophq/hoop/gateway/review"
	"github.com/hoophq/hoop/gateway/security/idp"
	"github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

const (
	PluginConfigEnvVarsParam = "plugin_config"
	slackMaxButtons          = 20
)

type (
	slackPlugin struct {
		reviewSvc   *review.Service
		idpProvider *idp.Provider
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

func New(reviewSvc *review.Service, idpProvider *idp.Provider) *slackPlugin {
	instances = map[string]*slack.SlackService{}
	mu = sync.RWMutex{}
	return &slackPlugin{
		reviewSvc:   reviewSvc,
		idpProvider: idpProvider,
	}
}

func (p *slackPlugin) Name() string { return plugintypes.PluginSlackName }

func (p *slackPlugin) startSlackServiceInstance(orgID string, slackConfig *slackConfig) error {
	storectx := storagev2.NewOrganizationContext(orgID)
	log.Infof("starting slack service instance for org %v", orgID)
	ss, err := slack.New(
		slackConfig.slackBotToken,
		slackConfig.slackAppToken,
		slackConfig.slackChannel,
		orgID,
		p.idpProvider.ApiURL,
		&eventCallback{orgID, storectx, p.idpProvider},
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
	orgList, err := pgorgs.New().FetchAllOrgs()
	if err != nil {
		return fmt.Errorf("failed listing organizations: %v", err)
	}

	for _, org := range orgList {
		pl, err := pgplugins.New().FetchOne(pgrest.NewOrgContext(org.ID), plugintypes.PluginSlackName)
		if err != nil {
			log.Errorf("failed retrieving plugin entity %v", err)
			continue
		}
		if pl == nil || pl.Config == nil {
			continue
		}
		if pl.OrgID == "" {
			log.Errorf("inconsistent state (org) for plugin slack")
			continue
		}
		slackConfig, err := parseSlackConfig(&types.PluginConfig{EnvVars: pl.Config.EnvVars})
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

func (p *slackPlugin) OnUpdate(oldState, newState *types.Plugin) error {
	slackInstance := getSlackServiceInstance(newState.OrgID)
	switch {
	// when it creates the plugin for the first time
	// it should only start it, if the client has sent a valid slack configuration
	case oldState == nil:
		if newSlackConfig, _ := parseSlackConfig(newState.Config); newSlackConfig != nil {
			if slackInstance != nil {
				slackInstance.Close()
			}
			return p.startSlackServiceInstance(newState.OrgID, newSlackConfig)
		}
	// when previous configuration doesn't exists
	case oldState.Config == nil:
		newSlackConfig, err := parseSlackConfig(newState.Config)
		if err != nil {
			return err
		}
		return p.startSlackServiceInstance(newState.OrgID, newSlackConfig)
	// when slack configuration changes
	default:
		if oldSlackConfig, _ := parseSlackConfig(oldState.Config); oldSlackConfig != nil {
			newSlackConfig, err := parseSlackConfig(newState.Config)
			if err != nil {
				return err
			}
			if oldSlackConfig.slackAppToken != newSlackConfig.slackAppToken ||
				oldSlackConfig.slackBotToken != newSlackConfig.slackBotToken {
				log.Warnf("configuration has changed, (re)starting slack instance %v", newState.OrgID)
				if slackInstance != nil {
					slackInstance.Close()
				}
				removeSlackServiceInstance(newState.OrgID)
				return p.startSlackServiceInstance(newState.OrgID, newSlackConfig)
			}
		}
	}
	return nil
}

// SendApprovedMessage sends a direct message to the owner of the review
// if it's approved
// func SendApprovedMessage(ctx *user.Context, rev *types.Review) {
// 	if rev.Status != types.ReviewStatusApproved {
// 		return
// 	}
// 	if slacksvc := getSlackServiceInstance(ctx.Org.Id); slacksvc != nil {
// 		if rev.ReviewOwner.SlackID != "" {
// 			log.Debugf("sending direct slack message to email=%v, slackid=%v",
// 				rev.ReviewOwner.Email, rev.ReviewOwner.SlackID)
// 			if err := slacksvc.SendDirectMessage(rev.Session, rev.ReviewOwner.SlackID); err != nil {
// 				log.Warn(err)
// 			}
// 		}
// 	}
// }

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

	rev, err := pgreview.New().FetchOneBySid(pctx, pctx.SID)
	if err != nil {
		return nil, plugintypes.InternalErr("internal error, failed fetching review", err)
	}
	if rev != nil {
		if rev.Status != types.ReviewStatusPending {
			return nil, nil
		}
		sreq.ID = rev.Id
		sreq.WebappURL = fmt.Sprintf("%s/sessions/%s", p.idpProvider.ApiURL, pctx.SID)
		sreq.ApprovalGroups = parseGroups(rev.ReviewGroupsData)
		if rev.AccessDuration > 0 {
			sreq.SessionTime = &rev.AccessDuration
		}
		sreq.Script = rev.Input
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

func parseSlackConfig(pconf *types.PluginConfig) (*slackConfig, error) {
	if pconf == nil {
		return nil, fmt.Errorf("missing required credentials for slack plugin")
	}
	slackBotToken, _ := base64.StdEncoding.DecodeString(pconf.EnvVars["SLACK_BOT_TOKEN"])
	slackAppToken, _ := base64.StdEncoding.DecodeString(pconf.EnvVars["SLACK_APP_TOKEN"])
	slackChannel, _ := base64.StdEncoding.DecodeString(pconf.EnvVars["SLACK_CHANNEL"])
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

func parseGroups(reviewGroups []types.ReviewGroup) []string {
	groups := make([]string, 0)
	for _, g := range reviewGroups {
		groups = append(groups, g.Group)
	}
	return groups
}

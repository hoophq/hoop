package slack

import (
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/slack"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var (
	ErrMissingRequiredCredentials = fmt.Errorf("missing required credentials for slack plugin")
)

const (
	PluginConfigEnvVarsParam = "plugin_config"
	SlackMaxButtons          = 20
)

type (
	slackPlugin struct {
		TransportReleaseConnection reviewapi.TransportReleaseConnectionFunc
		apiURL                     string

		// mu serializes starting and stopping services; running is what
		// each org's service was started with, for the replica sync to
		// compare against.
		mu      sync.Mutex
		running map[string]runningService
		// holder names this process in slack_socket_slots.
		holder string
	}

	// runningService is how an org's service was started: with which config,
	// and whether it opened a socket.
	runningService struct {
		cfg    slackConfig
		socket bool
	}
)

const (
	// replicaSyncEvery is how long a replica that did not serve a Slack
	// config write keeps running the previous config, and how often it
	// renews its socket slot. The write reaches only the replica that served
	// it (OnUpdate); every other one finds it here.
	replicaSyncEvery = 30 * time.Second
	// socketSlotTTL outlives two missed renewals, so a slow tick does not
	// hand the slot to another replica while this one still holds the socket.
	socketSlotTTL = 3 * replicaSyncEvery
)

func New(releaseConnFn reviewapi.TransportReleaseConnectionFunc) *slackPlugin {
	return &slackPlugin{
		TransportReleaseConnection: releaseConnFn,
		apiURL:                     appconfig.Get().ApiURL(),
		holder:                     uuid.NewString(),
	}
}

func (p *slackPlugin) Name() string { return plugintypes.PluginSlackName }

// startSlackServiceInstance starts the org's service, replacing the running
// one. Callers hold p.mu.
//
// The posted review messages live in the database, and only the replicas
// holding a socket slot open a socket; the rest post through the Web API.
// Slack hands each click to one open socket, so the replica handling a click
// is rarely the one that posted the message it rewrites.
func (p *slackPlugin) startSlackServiceInstance(orgID string, cfg *slackConfig) error {
	opts := []slack.Option{slack.WithMessageStoreIn(models.DB)}
	socket := p.holdSocketSlot(orgID)
	log.Infof("starting slack service instance for org %v, socket=%v", orgID, socket)
	ss, err := slack.New(
		cfg.slackBotToken,
		cfg.slackAppToken,
		cfg.slackChannel,
		orgID,
		p.apiURL,
		opts...,
	)
	if err != nil {
		return fmt.Errorf("failed starting slack service, err=%v", err)
	}
	// Swap before closing the old one, so a review filed meanwhile always
	// finds a service to post with.
	old := slack.GetServiceInstance(orgID)
	slack.SetServiceInstance(orgID, ss)
	if old != nil && old != ss {
		old.Close()
	}
	if p.running == nil {
		p.running = map[string]runningService{}
	}
	p.running[orgID] = runningService{cfg: *cfg, socket: socket}
	if !socket {
		return nil
	}
	reviewRespCh := make(chan *slack.MessageReviewResponse)
	go func() {
		defer close(reviewRespCh)
		if err := ss.ProcessEvents(reviewRespCh); err != nil {
			log.Errorf("failed processing slack events for org %v, reason=%v", orgID, err)
			p.markSocketDown(orgID, ss)
			return
		}
		log.Infof("done processing events for org %v", orgID)
		ss.Close()
		slack.RemoveServiceInstanceIf(orgID, ss)
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
	configs, _, err := slackConfigsByOrg()
	if err != nil {
		return err
	}
	p.mu.Lock()
	for orgID, cfg := range configs {
		if err := p.startSlackServiceInstance(orgID, &cfg); err != nil {
			log.Errorf("failed starting slack service for org %v, err=%v", orgID, err)
		}
	}
	p.mu.Unlock()
	go func() {
		for range time.Tick(replicaSyncEvery) {
			p.syncReplicas()
		}
	}()
	return nil
}

// slackConfigsByOrg reads the Slack config of every org that has a valid one.
// An org whose row cannot be read or parsed is logged and left out; unreadable
// names the ones whose row could not be read, which is not the same as having
// no config.
func slackConfigsByOrg() (configs map[string]slackConfig, unreadable map[string]bool, err error) {
	orgList, err := models.ListAllOrganizations()
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing organizations: %v", err)
	}
	configs, unreadable = map[string]slackConfig{}, map[string]bool{}
	for _, org := range orgList {
		pl, err := models.GetPluginByName(models.DB, org.ID, plugintypes.PluginSlackName)
		if err != nil && err != models.ErrNotFound {
			log.Errorf("failed retrieving plugin entity %v", err)
			unreadable[org.ID] = true
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
		configs[pl.OrgID] = *slackConfig
	}
	return configs, unreadable, nil
}

// syncReplicas makes this replica run each org's stored Slack config: it
// starts what is new, restarts what changed and stops what was removed. It
// also renews the socket slot, and restarts the service when the slot was won
// or lost. A config that fails to start is retried on the next tick.
func (p *slackPlugin) syncReplicas() {
	configs, unreadable, err := slackConfigsByOrg()
	if err != nil {
		log.Warnf("failed reading the slack configs to sync, reason=%v", err)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for orgID, cfg := range configs {
		cur, ok := p.running[orgID]
		wantSocket := p.holdSocketSlot(orgID)
		if ok && cur.cfg == cfg && cur.socket == wantSocket {
			continue
		}
		log.Infof("slack config or socket slot changed, (re)starting slack instance %v", orgID)
		if err := p.startSlackServiceInstance(orgID, &cfg); err != nil {
			log.Warnf("failed starting slack service for org %v, err=%v", orgID, err)
		}
	}
	for orgID := range p.running {
		// A row that failed to read is a database blip, not a removed config.
		if _, ok := configs[orgID]; !ok && !unreadable[orgID] {
			log.Infof("slack config removed on another replica, stopping slack instance %v", orgID)
			p.stopSlackServiceInstance(orgID)
		}
	}
}

// stopSlackServiceInstance stops the org's service and frees its socket slot.
// Callers hold p.mu.
func (p *slackPlugin) stopSlackServiceInstance(orgID string) {
	if ss := slack.GetServiceInstance(orgID); ss != nil {
		ss.Close()
		slack.RemoveServiceInstanceIf(orgID, ss)
	}
	delete(p.running, orgID)
	if err := models.ReleaseSlackSocketSlot(models.DB, orgID, p.holder); err != nil {
		log.Warnf("failed releasing the slack socket slot for org %v, reason=%v", orgID, err)
	}
}

// markSocketDown records that ss's socket stopped, so the next sync restarts
// the service instead of renewing a slot with no socket behind it. A service
// already replaced or removed is left alone: its socket ending is the
// restart, not a failure.
func (p *slackPlugin) markSocketDown(orgID string, ss *slack.SlackService) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r, ok := p.running[orgID]; ok && slack.GetServiceInstance(orgID) == ss {
		r.socket = false
		p.running[orgID] = r
	}
}

// holdSocketSlot renews or claims this replica's socket slot for the org. A
// database error keeps what the replica has now: dropping every socket on a
// database blip would leave no replica taking clicks.
func (p *slackPlugin) holdSocketSlot(orgID string) bool {
	held, err := models.HoldSlackSocketSlot(models.DB, orgID, p.holder,
		appconfig.Get().SlackSocketSlots(), socketSlotTTL)
	if err != nil {
		log.Warnf("failed holding a slack socket slot for org %v, reason=%v", orgID, err)
		return p.running[orgID].socket
	}
	return held
}

func (p *slackPlugin) OnUpdate(oldState, newState plugintypes.PluginResource) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	slackInstance := slack.GetServiceInstance(newState.GetOrgID())
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
				slack.RemoveServiceInstance(newState.GetOrgID())
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
	if slacksvc := slack.GetServiceInstance(orgID); slacksvc != nil {
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
	slackSvc := slack.GetServiceInstance(pctx.OrgID)
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
		return nil, plugintypes.InternalErr("internal error, failed fetching approval request", err)
	}
	if rev != nil {
		if rev.Status != models.ReviewStatusPending {
			return nil, nil
		}
		reviewInput, err := rev.GetBlobInput()
		if err != nil {
			return nil, plugintypes.InternalErr("internal error, failed fetching approval request input", err)
		}
		sreq.ID = rev.ID
		sreq.WebappURL = fmt.Sprintf("%s/sessions/%s", p.apiURL, rev.SessionID)
		sreq.ApprovalGroups = ParseGroups(rev.ReviewGroups)
		if rev.AccessDurationSec > 0 {
			ad := time.Duration(rev.AccessDurationSec) * time.Second
			sreq.SessionTime = &ad
		}
		sreq.Script = reviewInput

		session, serr := models.GetSessionByID(pctx.OrgID, pctx.SID)
		switch {
		case serr != nil:
			// Best-effort: the review message must still go out without the
			// analysis block, but a load failure is not the same as "no
			// analysis" and should be visible when debugging a missing block.
			log.With("sid", pctx.SID).Warnf("failed loading session for slack ai analysis block, reason=%v", serr)
		case session != nil && session.AIAnalysis != nil:
			sreq.AIRiskLevel = session.AIAnalysis.RiskLevel
			sreq.AITitle = session.AIAnalysis.Title
			sreq.AISummary = session.AIAnalysis.Summary
			sreq.AIExplanation = session.AIAnalysis.Explanation
		}
	}

	if sreq.WebappURL == "" || len(sreq.ApprovalGroups) == 0 || len(sreq.ApprovalGroups) >= SlackMaxButtons {
		log.With("sid", pctx.SID).Infof("no review message to process, has-webapp-url=%v, approval-groups=%v/%v",
			sreq.WebappURL != "", len(sreq.ApprovalGroups), SlackMaxButtons)
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

func ParseGroups(reviewGroups []models.ReviewGroups) []string {
	groups := make([]string, 0)
	for _, g := range reviewGroups {
		groups = append(groups, g.GroupName)
	}
	return groups
}

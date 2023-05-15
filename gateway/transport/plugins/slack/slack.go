package slack

import (
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/review/jit"
	slackservice "github.com/runopsio/hoop/gateway/slack"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
)

const (
	PluginConfigEnvVarsParam = "plugin_config"
	slackMaxButtons          = 20
)

type (
	slackPlugin struct {
		reviewSvc *review.Service
		jitSvc    *jit.Service
		userSvc   *user.Service
		apiURL    string
	}
)

var instances map[string]*slackservice.SlackService
var mu sync.RWMutex

func getSlackServiceInstance(orgID string) *slackservice.SlackService {
	mu.Lock()
	defer mu.Unlock()
	return instances[orgID]
}

func New(reviewSvc *review.Service, jitSvc *jit.Service, userSvc *user.Service, apiURL string) *slackPlugin {
	instances = map[string]*slackservice.SlackService{}
	mu = sync.RWMutex{}
	return &slackPlugin{
		reviewSvc: reviewSvc,
		jitSvc:    jitSvc,
		userSvc:   userSvc,
		apiURL:    apiURL,
	}
}

func (p *slackPlugin) Name() string                          { return plugintypes.PluginSlackName }
func (p *slackPlugin) OnStartup(_ plugintypes.Context) error { return nil }
func (p *slackPlugin) OnConnect(pctx plugintypes.Context) error {
	log.Infof("session=%v | slack | processing on-connect", pctx.SID)
	mu.Lock()
	defer mu.Unlock()
	sconf, err := parseSlackConfig(pctx.ParamsData[PluginConfigEnvVarsParam])
	if err != nil {
		return err
	}

	if ss, ok := instances[pctx.OrgID]; ok {
		if ss.BotToken() == sconf.slackBotToken {
			return nil
		}
		log.Warnf("slack configuration has changed, closing instance/org %v", pctx.OrgID)
		// configuration has changed, clean up
		ss.Close()
		delete(instances, pctx.OrgID)
	}

	log.Infof("starting slack service instance for instance/org %v", pctx.OrgID)
	ss, err := slackservice.New(
		sconf.slackBotToken,
		sconf.slackAppToken,
		sconf.slackChannel,
		pctx.OrgID,
	)
	if err != nil {
		return err
	}
	instances[pctx.OrgID] = ss
	reviewRespCh := make(chan *slackservice.MessageReviewResponse)
	go func() {
		defer close(reviewRespCh)
		if err := ss.ProcessEvents(reviewRespCh); err != nil {
			log.Errorf("failed processing slack events for org %v, reason=%v", pctx.OrgID, err)
			return
		}
		log.Infof("done processing events for org %v", pctx.OrgID)
		mu.Lock()
		defer mu.Unlock()
		ss.Close()
		delete(instances, pctx.OrgID)
	}()

	// response channel
	go func() {
		for resp := range reviewRespCh {
			p.processEventResponse(&event{ss, resp, pctx.OrgID})
		}
		log.Infof("close response channel for org %v", pctx.OrgID)
	}()
	return nil
}

func (p *slackPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pkt.Type != pbagent.SessionOpen {
		return nil, nil
	}
	log.Infof("executing slack on-receive ...")

	sreq := &slackservice.MessageReviewRequest{
		Name:           pctx.UserName,
		Email:          pctx.UserEmail,
		Connection:     pctx.ConnectionName,
		ConnectionType: pctx.ConnectionType,
		SessionID:      pctx.SID,
		UserGroups:     pctx.UserGroups,
	}

	rev, err := p.reviewSvc.FindBySessionID(pctx.SID)
	if err != nil {
		return nil, plugintypes.InternalErr("internal error, failed fetching review", err)
	}
	if rev != nil {
		if rev.Status != review.StatusPending {
			return nil, nil
		}
		sreq.ID = rev.Id
		sreq.WebappURL = fmt.Sprintf("%s/plugins/reviews/%s", p.apiURL, rev.Id)
		sreq.ApprovalGroups = parseGroups(rev.ReviewGroups)
		if rev.AccessDuration > 0 {
			sreq.SessionTime = &rev.AccessDuration
		}
		sreq.Script = truncateString(rev.Input)
	}

	if sreq.WebappURL == "" || len(sreq.ApprovalGroups) == 0 || len(sreq.ApprovalGroups) >= slackMaxButtons {
		log.With("session", pctx.SID).Infof("no review message to process")
		return nil, nil
	}

	if ss := getSlackServiceInstance(pctx.OrgID); ss != nil {
		log.With("session", pctx.SID).Infof("sending slack review message, conn=%v, jit=%v",
			sreq.Connection, sreq.SessionTime != nil)
		if err := ss.SendMessageReview(sreq); err != nil {
			log.With("session", pctx.SID).Errorf("failed sending slack review message, reason=%v", err)
		}
	}
	return nil, nil
}

func (p *slackPlugin) OnDisconnect(_ plugintypes.Context, _ error) error { return nil }
func (p *slackPlugin) OnShutdown()                                       {}

type slackConfig struct {
	slackBotToken string
	slackAppToken string
	slackChannel  string
}

func parseSlackConfig(envVarsObj any) (*slackConfig, error) {
	pluginEnvVar, _ := envVarsObj.(map[string]string)
	if len(pluginEnvVar) == 0 {
		return nil, fmt.Errorf("missing required credentials for slack plugin")
	}
	slackBotToken, _ := base64.StdEncoding.DecodeString(pluginEnvVar["SLACK_BOT_TOKEN"])
	slackAppToken, _ := base64.StdEncoding.DecodeString(pluginEnvVar["SLACK_APP_TOKEN"])
	slackChannel, _ := base64.StdEncoding.DecodeString(pluginEnvVar["SLACK_CHANNEL"])
	sc := slackConfig{
		slackBotToken: string(slackBotToken),
		slackAppToken: string(slackAppToken),
		slackChannel:  string(slackChannel),
	}
	if sc.slackBotToken == "" || sc.slackAppToken == "" || sc.slackChannel == "" {
		return nil, fmt.Errorf("missing required slack credentials")
	}
	return &sc, nil
}

func parseGroups(reviewGroups []review.Group) []string {
	groups := make([]string, 0)
	for _, g := range reviewGroups {
		groups = append(groups, g.Group)
	}
	return groups
}

func truncateString(s string) string {
	maxSlackSize := 2750
	if len(s) > maxSlackSize {
		return s[:maxSlackSize]
	}
	return s
}

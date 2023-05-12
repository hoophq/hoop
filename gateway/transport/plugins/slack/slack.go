package slack

import (
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/review/jit"
	slackservice "github.com/runopsio/hoop/gateway/slack"
	transporterr "github.com/runopsio/hoop/gateway/transport/errors"
	"github.com/runopsio/hoop/gateway/user"
)

const (
	Name                     = "slack"
	PluginConfigEnvVarsParam = "plugin_config"
	slackMaxButtons          = 20
)

type (
	slackPlugin struct {
		name      string
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
		name:      Name,
		reviewSvc: reviewSvc,
		jitSvc:    jitSvc,
		userSvc:   userSvc,
		apiURL:    apiURL,
	}
}

func (p *slackPlugin) Name() string                         { return p.name }
func (p *slackPlugin) OnStartup(config plugin.Config) error { return nil }
func (p *slackPlugin) OnConnect(config plugin.Config) error {
	log.Infof("session=%v | slack | processing on-connect", config.SessionId)
	mu.Lock()
	defer mu.Unlock()
	sconf, err := parseSlackConfig(config.ParamsData[PluginConfigEnvVarsParam])
	if err != nil {
		return err
	}

	if ss, ok := instances[config.Org]; ok {
		if ss.BotToken() == sconf.slackBotToken {
			return nil
		}
		log.Warnf("slack configuration has changed, closing instance/org %v", config.Org)
		// configuration has changed, clean up
		ss.Close()
		delete(instances, config.Org)
	}

	log.Infof("starting slack service instance for instance/org %v", config.Org)
	ss, err := slackservice.New(
		sconf.slackBotToken,
		sconf.slackAppToken,
		sconf.slackChannel,
		config.Org,
	)
	if err != nil {
		return err
	}
	instances[config.Org] = ss
	reviewRespCh := make(chan *slackservice.MessageReviewResponse)
	go func() {
		defer close(reviewRespCh)
		if err := ss.ProcessEvents(reviewRespCh); err != nil {
			log.Errorf("failed processing slack events for org %v, reason=%v", config.Org, err)
			return
		}
		log.Infof("done processing events for org %v", config.Org)
		mu.Lock()
		defer mu.Unlock()
		ss.Close()
		delete(instances, config.Org)
	}()

	// response channel
	go func() {
		for resp := range reviewRespCh {
			p.processEventResponse(&event{ss, resp, config.Org})
		}
		log.Infof("close response channel for org %v", config.Org)
	}()
	return nil
}

func (p *slackPlugin) OnReceive(pconf plugin.Config, config []string, pkt *pb.Packet) error {
	if pkt.Type != pbagent.SessionOpen {
		return nil
	}
	log.Infof("executing slack on-receive ...")

	sreq := &slackservice.MessageReviewRequest{
		Name:           pconf.UserName,
		Email:          pconf.UserEmail,
		Connection:     pconf.ConnectionName,
		ConnectionType: pconf.ConnectionType,
		SessionID:      pconf.SessionId,
		UserGroups:     pconf.UserGroups,
	}

	rev, err := p.reviewSvc.FindBySessionID(pconf.SessionId)
	if err != nil {
		return transporterr.Internal("internal error, failed fetching review", err)
	}
	if rev != nil {
		if rev.Status != review.StatusPending {
			return nil
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
		log.With("session", pconf.SessionId).Infof("no review message to process")
		return nil
	}

	if ss := getSlackServiceInstance(pconf.Org); ss != nil {
		log.With("session", pconf.SessionId).Infof("sending slack review message, conn=%v, jit=%v",
			sreq.Connection, sreq.SessionTime != nil)
		if err := ss.SendMessageReview(sreq); err != nil {
			log.With("session", pconf.SessionId).Errorf("failed sending slack review message, reason=%v", err)
		}
	}
	return nil
}

func (p *slackPlugin) OnDisconnect(config plugin.Config, errMsg error) error { return nil }
func (p *slackPlugin) OnShutdown()                                           {}

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

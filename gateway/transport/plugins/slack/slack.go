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
	_, ok := instances[config.Org]
	if !ok {
		sconf, err := parsePluginConfigEnvVars(config.ParamsData[PluginConfigEnvVarsParam])
		if err != nil {
			return err
		}

		log.Infof("starting slack service instance for org %v", config.Org)
		ss, err := slackservice.New(sconf.slackBotToken, sconf.slackAppToken, sconf.slackChannel)
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
			log.Infof("done processing events for org %v, reason=%v", config.Org)
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
	// TODO check if the plugin configuration has been changed
	// close the websocket and start a new connection with the new config.
	// ss.Close()

	return nil
}

func (p *slackPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	if pkt.Type != pbagent.SessionOpen {
		return nil
	}

	sreq := &slackservice.MessageReviewRequest{
		Name:       pluginConfig.UserName,
		Email:      pluginConfig.UserEmail,
		Connection: pluginConfig.ConnectionName,
		Type:       pluginConfig.ConnectionType,
		SessionID:  pluginConfig.SessionId,
		UserGroups: pluginConfig.UserGroups,
	}

	if reviewEnc, hasReview := pkt.Spec[pb.SpecReviewDataKey]; hasReview {
		var rev review.Review
		if err := pb.GobDecodeInto(reviewEnc, &rev); err != nil {
			log.With("session", pluginConfig.SessionId).Errorf("failed to decode review, err=%v", err)
			return nil
		}
		if rev.Status != review.StatusPending {
			return nil
		}
		sreq.ID = rev.Id
		sreq.WebappURL = fmt.Sprintf("%s/plugins/reviews/%s", p.apiURL, rev.Id)
		sreq.ApprovalGroups = parseGroups(rev.ReviewGroups)
		sreq.Script = truncateString(rev.Input)
	}

	if status, hasJit := pkt.Spec[pb.SpecJitStatus]; hasJit && sreq.ID == "" {
		if jit.Status(status) != jit.StatusPending {
			return nil
		}
		j, err := p.jitSvc.FindBySessionID(pluginConfig.SessionId)
		if err != nil {
			log.With("session", pluginConfig.SessionId).Errorf("failed obtaining jit, err=%v", err)
			return nil
		}
		if j == nil {
			return nil
		}
		sreq.ID = j.Id
		sreq.WebappURL = fmt.Sprintf("%s/plugins/jits/%s", p.apiURL, j.Id)
		sreq.ApprovalGroups = parseJitGroups(j.JitGroups)
		sreq.SessionTime = &j.Time
	}

	if sreq.WebappURL == "" || len(sreq.ApprovalGroups) == 0 || len(sreq.ApprovalGroups) >= slackMaxButtons {
		return nil
	}

	if ss := getSlackServiceInstance(pluginConfig.Org); ss != nil {
		log.With("session", pluginConfig.SessionId).Infof("sending slack review message, conn=%v, jit=%v",
			sreq.Connection, sreq.SessionTime != nil)
		if err := ss.SendMessageReview(sreq); err != nil {
			log.With("session", pluginConfig.SessionId).Errorf("failed sending slack review message, reason=%v", err)
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

func parsePluginConfigEnvVars(envVarsObj any) (*slackConfig, error) {
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

func parseJitGroups(reviewGroups []jit.Group) []string {
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

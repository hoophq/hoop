package webhooks

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/appconfig"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	svix "github.com/svix/svix-webhooks/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type plugin struct {
	client   *svix.Svix
	appStore memory.Store
}

func New() *plugin {
	if webhookAppKey := appconfig.Get().WebhookAppKey(); webhookAppKey != "" {
		log.Infof("loaded webhook app key with success")
		webhookAppUrl := appconfig.Get().WebhookAppURL()
		return &plugin{svix.New(webhookAppKey, &svix.SvixOptions{ServerUrl: webhookAppUrl}), memory.New()}
	}
	return &plugin{}
}

func (p *plugin) Name() string { return plugintypes.PluginWebhookName }
func (p *plugin) OnStartup(_ plugintypes.Context) error {
	if p.client == nil {
		return nil
	}
	ctxtimeout, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()
	resp, err := p.client.Application.List(ctxtimeout, nil)
	if err != nil {
		return fmt.Errorf("failed listing apps on webhook service provider, err=%v", err)
	}
	for _, out := range resp.GetData() {
		log.Infof("found app=%s, created_at=%s", out.Name, out.CreatedAt.Format(time.RFC3339))
		if out.Uid.IsSet() {
			log.Infof("storing app in memory, app=%s, uid=%s", out.Name, *out.Uid.Get())
			p.appStore.Set(*out.Uid.Get(), nil)
			continue
		}
	}

	return nil
}

// OnUpdate will create the app on svix if it doesn't exists
func (p *plugin) OnUpdate(old, new *types.Plugin) error {
	if new != nil && p.client != nil {
		shortOrgID := new.OrgID[0:8]
		ctxtimeout, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
		defer cancelFn()
		out, err := p.client.Application.GetOrCreate(ctxtimeout, &svix.ApplicationIn{
			Name: shortOrgID,
			Uid:  *svix.NullableString(&new.OrgID),
		})
		if err != nil {
			return fmt.Errorf("fail creating webhook plugin application, reason=%v", err)
		}
		p.appStore.Set(new.OrgID, nil)
		log.Infof("application created with success for organization %s, id=%s, uid=%v",
			new.OrgID, out.Id, out.Uid)
	}
	return nil
}

func (p *plugin) OnConnect(ctx plugintypes.Context) error {
	isWebhookSet := appconfig.Get().WebhookAppKey() != ""
	if ctx.OrgLicenseType == license.OSSType && isWebhookSet {
		return status.Error(codes.FailedPrecondition, license.ErrWebhooksUnsupported.Error())
	}
	return nil
}

func (p *plugin) OnReceive(ctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if p.hasLoadedApp(ctx.OrgID) {
		switch pkt.Type {
		case pbagent.SessionOpen:
			p.processSessionOpenEvent(ctx, pkt)
			p.processReviewCreateEvent(ctx)
		case pbclient.SessionClose:
			p.processSessionCloseEvent(ctx, pkt)
		}
	}
	return nil, nil
}

// https://learn.microsoft.com/en-us/microsoftteams/platform/task-modules-and-cards/cards/cards-format?tabs=adaptive-md%2Cdesktop%2Cdesktop1%2Cdesktop2%2Cconnector-html#codeblock-in-adaptive-cards
func parseLangCodeBlock(connType, connSubtype string) string {
	switch connType {
	case "database":
		return "SQL"
	case "application":
		switch connSubtype {
		case "go", "java", "perl":
			return strings.ToTitle(connSubtype)
		case "json":
			return "JSON"
		case "xml":
			return "XML"
		case "powershell":
			return "PowerShell"
		case "php":
			return "PHP"
		default:
			return "Bash"
		}
	case "custom":
		return "Bash"
	}
	return "PlainText"
}

func (p *plugin) processReviewCreateEvent(ctx plugintypes.Context) {
	rev, err := pgreview.New().FetchOneBySid(ctx, ctx.SID)
	if err != nil {
		log.Warnf("failed obtaining review, err=%v", err)
		return
	}
	if rev == nil {
		return
	}
	// it's recommended to sent events up to 20KB (Microsoft Teams)
	// that's why we truncated the input payload
	if len(rev.Input) > maxInputSize {
		rev.Input = rev.Input[0:maxInputSize]
	}
	appID := ctx.OrgID
	ctxtimeout, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()

	eventID := uuid.NewString()
	accessDuration := rev.AccessDuration.String()
	if accessDuration == "0s" {
		accessDuration = "`-`"
	}
	apiURL := appconfig.Get().FullApiURL()
	svixPayload := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"msteams": map[string]any{"width": "full"},
				"body": []map[string]any{
					{
						"type":      "TextBlock",
						"text":      "Session Created",
						"size":      "Large",
						"separator": true,
						"weight":    "Bolder",
					},
					{
						"type":      "TextBlock",
						"text":      fmt.Sprintf("[%s](%s/sessions/%s)", rev.Session, apiURL, rev.Session),
						"separator": false,
					},
					{
						"spacing":   "ExtraLarge",
						"separator": true,
						"type":      "FactSet",
						"facts": []map[string]any{
							{
								"title": "Created By",
								"value": fmt.Sprintf("%s | %s", rev.ReviewOwner.Name, rev.ReviewOwner.Email),
							},
							{
								"title": "Approval Groups",
								"value": fmt.Sprintf("%q", parseGroups(rev.ReviewGroupsData)),
							},
							{
								"title": "Session Time",
								"value": accessDuration,
							},
							{
								"title": "Connection",
								"value": rev.Connection.Name,
							},
						},
					},
					{
						"type":      "Container",
						"separator": false,
						"style":     "default",
						"bleed":     false,
						"items": []map[string]any{{
							"type":        "CodeBlock",
							"codeSnippet": rev.Input,
							"language":    parseLangCodeBlock(ctx.ConnectionType, ctx.ConnectionSubType),
						}},
					},
				},
			},
		}},
	}
	out, err := p.client.Message.Create(ctxtimeout, appID, &svix.MessageIn{
		EventType: eventMSTeamsReviewCreateType,
		EventId:   *svix.NullableString(func() *string { v := eventID; return &v }()),
		Payload:   svixPayload,
	})
	if err != nil {
		log.With("appid", appID).Warnf("failed sending webhook event to remote source, err=%v", err)
		return
	}
	if out != nil {
		log.With("appid", appID).Infof("sent webhook with success, id=%s, event=%s, eventid=%s",
			out.Id, out.EventType, eventID)
	}

}

func (p *plugin) processSessionOpenEvent(ctx plugintypes.Context, pkt *pb.Packet) {
	appID := ctx.OrgID
	ctxtimeout, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()

	clientArgs := decodeClientArgs(pkt)
	clientEnvVars := decodeClientEnvVars(pkt)

	// it's recommended to sent events up to 40KB
	// that's why we truncated the input payload
	isInputTruncated := len(pkt.Payload) > maxInputSize
	truncatedStdinInput := make([]byte, len(pkt.Payload))
	_ = copy(truncatedStdinInput, pkt.Payload)
	if len(truncatedStdinInput) > maxInputSize {
		truncatedStdinInput = truncatedStdinInput[0:maxInputSize]
	}
	var connectionEnvs []string
	for key := range ctx.ConnectionSecret {
		connectionEnvs = append(connectionEnvs, key)
	}
	var inputEventEnvs []string
	for key := range clientEnvVars {
		inputEventEnvs = append(inputEventEnvs, key)
	}
	fullCommand := append(ctx.ConnectionCommand, clientArgs...)
	eventID := uuid.NewString()
	out, err := p.client.Message.Create(ctxtimeout, appID, &svix.MessageIn{
		EventType: eventSessionOpenType,
		EventId:   *svix.NullableString(func() *string { v := eventID; return &v }()),
		// TODO: use openapi schema
		Payload: map[string]any{
			"event_type":      eventSessionOpenType,
			"id":              ctx.SID,
			"user_id":         ctx.UserID,
			"user_email":      ctx.UserEmail,
			"connection":      ctx.ConnectionName,
			"connection_type": ctx.ConnectionType,
			"connection_envs": connectionEnvs,
			// it will be encoded to base64 automatically
			"input":              truncatedStdinInput,
			"is_input_truncated": isInputTruncated,
			"input_size":         len(pkt.Payload),
			"input_envs":         inputEventEnvs,
			"has_input_args":     len(clientArgs) > 0,
			"command":            fullCommand,
			"verb":               ctx.ClientVerb,
		},
	})
	if err != nil {
		log.With("appid", appID).Warnf("failed sending webhook event to remote source, err=%v", err)
		return
	}
	if out != nil {
		log.With("appid", appID).Infof("sent webhook with success, id=%s, event=%s, eventid=%s",
			out.Id, out.EventType, eventID)
	}
}

func (p *plugin) processSessionCloseEvent(ctx plugintypes.Context, pkt *pb.Packet) {
	appID := ctx.OrgID
	exitCode := -100
	exitCodeInt, err := strconv.Atoi(string(pkt.Spec[pb.SpecClientExitCodeKey]))
	if err == nil {
		exitCode = exitCodeInt
	}
	var exitErr *string
	if len(pkt.Payload) > 0 {
		exitErr = func() *string { v := string(pkt.Payload); return &v }()
	}
	eventID := uuid.NewString()
	ctxtimeout, cancelFn := context.WithTimeout(context.Background(), time.Second*3)
	defer cancelFn()
	out, err := p.client.Message.Create(ctxtimeout, appID, &svix.MessageIn{
		EventType: eventSessionCloseType,
		EventId:   *svix.NullableString(func() *string { v := eventID; return &v }()),
		// TODO: use openapi schema
		Payload: map[string]any{
			"event_type": eventSessionCloseType,
			"id":         ctx.SID,
			"exit_code":  exitCode,
			"exit_err":   exitErr,
		},
	})
	if err != nil {
		log.With("appid", appID).Warnf("failed sending webhook event to remote source, event=%s, err=%v",
			eventSessionCloseType, err)
		return
	}

	if out != nil {
		log.With("appid", appID).Infof("sent webhook with success, id=%s, event=%s, eventid=%s",
			out.Id, out.EventType, eventID)
	}
}

func (p *plugin) OnDisconnect(_ plugintypes.Context, _ error) error { return nil }
func (p *plugin) OnShutdown()                                       {}

// hasLoadedApp check if the app is in memory
func (p *plugin) hasLoadedApp(orgID string) bool {
	if p.client != nil {
		return p.appStore.Has(orgID)
	}
	return false
}

func decodeClientArgs(pkt *pb.Packet) []string {
	var clientArgs []string
	if pkt.Spec != nil {
		encArgs := pkt.Spec[pb.SpecClientExecArgsKey]
		if len(encArgs) > 0 {
			if err := pb.GobDecodeInto(encArgs, &clientArgs); err != nil {
				log.With("plugin", "webhooks").Warnf("failed decoding client args, err=%v", err)
			}
		}
	}
	return clientArgs
}

func decodeClientEnvVars(pkt *pb.Packet) map[string]string {
	clientEnvVars := map[string]string{}
	if pkt.Spec != nil {
		encEnvVars := pkt.Spec[pb.SpecClientExecEnvVar]
		if len(encEnvVars) > 0 {
			if err := pb.GobDecodeInto(encEnvVars, &clientEnvVars); err != nil {
				log.With("plugin", "webhooks").Warnf("failed decoding client env vars, err=%v", err)
			}
		}
	}
	return clientEnvVars
}

func parseGroups(reviewGroups []types.ReviewGroup) []string {
	groups := make([]string, 0)
	for _, g := range reviewGroups {
		groups = append(groups, g.Group)
	}
	return groups
}

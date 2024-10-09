package jira

import (
	"encoding/base64"
	"fmt"
	"libhoop/log"
	"os"
	"slices"
	"strings"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/jira"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
	sessionstorage "github.com/hoophq/hoop/gateway/storagev2/session"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

type plugin struct{}

type sessionParseOption struct {
	withLineBreak bool
	events        []string
}

func parseSessionToFile(s *types.Session, opts sessionParseOption) []byte {
	output := []byte{}
	for _, eventList := range s.EventStream {
		event := eventList.(types.SessionEventStream)
		eventType, _ := event[1].(string)
		eventData, _ := base64.StdEncoding.DecodeString(event[2].(string))

		if !slices.Contains(opts.events, eventType) {
			continue
		}

		switch eventType {
		case "i":
			output = append(output, eventData...)
		case "o", "e":
			output = append(output, eventData...)
		}
		if opts.withLineBreak {
			output = append(output, '\n')
		}
	}
	return output
}

func New() *plugin { return &plugin{} }

func (r *plugin) Name() string                            { return plugintypes.PluginJiraName }
func (r *plugin) OnStartup(ctx plugintypes.Context) error { return nil }
func (p *plugin) OnUpdate(_, _ *types.Plugin) error       { return nil }
func (r *plugin) OnConnect(ctx plugintypes.Context) error {
	go func() {
		time.Sleep(time.Second * 2)
		session, err := pgsession.New().FetchOne(ctx, ctx.SID)
		if err != nil {
			log.Warnf("failed obtaining session, err=%v", err)
			return
		}

		review, err := pgreview.New().FetchOneBySid(ctx, ctx.SID)
		if err != nil {
			log.Warnf("failed obtaining review, err=%v", err)
			return
		}

		if session.JiraIssue == "" {
			log.Info("there's no jira issue for this session, creating one")
			reviewGroups := make([]string, 0)
			if review != nil && len(review.ReviewGroupsData) > 0 {
				reviewGroups = make([]string, len(review.ReviewGroupsData))
				for i, r := range review.ReviewGroupsData {
					reviewGroups[i] = r.Group
				}
			}

			descriptionContent := []interface{}{
				jira.ParagraphBlock(
					jira.StrongTextBlock("user: "),
					jira.TextBlock(ctx.UserName),
				),
				jira.ParagraphBlock(
					jira.StrongTextBlock("connection: "),
					jira.TextBlock(ctx.ConnectionName),
				),
			}

			if len(reviewGroups) > 0 {
				descriptionContent = append(descriptionContent,
					jira.ParagraphBlock(
						jira.StrongTextBlock("reviewers groups: "),
						jira.TextBlock(strings.Join(reviewGroups, ", ")),
					),
				)
			}

			if ctx.ClientVerb == pb.ClientVerbExec && ctx.ClientOrigin == pb.ConnectionOriginClientAPI {
				descriptionContent = append(descriptionContent,
					jira.ParagraphBlock(
						jira.StrongTextBlock("script: "),
					),
					jira.CodeSnippetBlock(session.Script["data"]),
				)
			}

			descriptionContent = append(descriptionContent,
				jira.ParagraphBlock(
					jira.StrongTextBlock("session link: "),
					jira.LinkBlock(
						fmt.Sprintf("%v/sessions/%v", os.Getenv("API_URL"), session.ID),
						fmt.Sprintf("%v/sessions/%v", os.Getenv("API_URL"), session.ID),
					),
				),
			)

			if err := jira.CreateIssueSimple(ctx.OrgID, "Hoop session", "Task", ctx.SID, descriptionContent); err != nil {
				log.Warnf("failed creating jira issue, err=%v", err)
			}
		}
	}()

	return nil
}

func (r *plugin) OnReceive(ctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	return nil, nil
}
func (r *plugin) OnDisconnect(ctx plugintypes.Context, _ error) error {
	go func() {
		time.Sleep(time.Second * 5)
		session, err := sessionstorage.FindOne(ctx, ctx.SID)
		if err != nil {
			log.Warnf("failed obtaining session, err=%v", err)
			return
		}

		events := []string{"o", "e"}
		if ctx.ClientVerb == pb.ClientVerbConnect && ctx.ConnectionType == "database" {
			events = []string{"i"}
		} else if ctx.ClientVerb == pb.ClientVerbConnect && ctx.ConnectionType == "custom" {
			events = []string{"i", "o", "e"}
		}

		payload := parseSessionToFile(session, sessionParseOption{withLineBreak: true, events: events})
		if ctx.ClientOrigin == pb.ConnectionOriginClient &&
			ctx.ClientVerb == pb.ClientVerbExec &&
			ctx.ConnectionType == "custom" {
			payload = parseSessionToFile(session, sessionParseOption{withLineBreak: false, events: events})
		}

		payloadLength := len(payload)
		if payloadLength > 5000 {
			payloadLength = 5000
		}

		descriptionContent := []interface{}{
			jira.ParagraphBlock(
				jira.TextBlock("The session was executed with the response: \n"),
			),
			jira.CodeSnippetBlock(string(payload[0:payloadLength])),
		}

		jira.UpdateJiraIssueDescription(ctx.OrgID, session.JiraIssue, descriptionContent)
	}()

	return nil
}
func (r *plugin) OnShutdown() {}

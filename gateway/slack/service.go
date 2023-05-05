package slack

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

type SlackService struct {
	apiClient     *slack.Client
	socketClient  *socketmode.Client
	slackChannel  string
	slackBotToken string
	instanceID    string
	ctx           context.Context
	cancelFn      context.CancelFunc
}

const (
	reviewIDMetadataKey  = "review_id"
	sessionIDMetadataKey = "session_id"
	EventKindReview      = "review"
	EventKindJit         = "jit"
)

func New(slackBotToken, slackAppToken, slackChannel, instanceID string) (*SlackService, error) {
	apiClient := slack.New(
		slackBotToken,
		// slack.OptionDebug(true),
		// slack.OptionLog(log.New(os.Stdout, "api: ", log.Lshortfile|log.LstdFlags)),
		slack.OptionAppLevelToken(slackAppToken),
	)
	_, err := apiClient.AuthTest()
	if err != nil {
		return nil, fmt.Errorf("fail to validate slack bot token authentication, err=%v", err)
	}
	socketClient := socketmode.New(
		apiClient,
		// socketmode.OptionDebug(true),
		// socketmode.OptionLog(log.New(os.Stdout, "socketmode: ", log.Lshortfile|log.LstdFlags)),
	)
	ctx, cancelFn := context.WithCancel(context.Background())
	return &SlackService{
		apiClient:    apiClient,
		socketClient: socketClient,
		slackChannel: slackChannel,
		instanceID:   instanceID,
		ctx:          ctx,
		cancelFn:     cancelFn}, nil
}

func (s *SlackService) Close()           { s.cancelFn() }
func (s *SlackService) BotToken() string { return s.slackBotToken }

type MessageReviewRequest struct {
	ID             string
	Name           string
	Email          string
	UserGroups     []string
	ApprovalGroups []string
	Connection     string
	Type           string
	Script         string
	SessionTime    *time.Duration
	WebappURL      string
	SessionID      string
}

type MessageReviewResponse struct {
	ID        string
	EventKind string
	Status    string
	SessionID string
	SlackID   string
	GroupName string

	item slack.InteractionCallback
}

func (m *MessageReviewRequest) sessionTime() string {
	if m.SessionTime != nil {
		minutes := m.SessionTime.Minutes()
		switch {
		case minutes < 60:
			return fmt.Sprintf("%v minute(s)", minutes)
		default:
			return fmt.Sprintf("%.2f hour(s)", minutes/60)
		}
	}
	return "-"
}

func (s *SlackService) SendMessageReview(msg *MessageReviewRequest) error {
	title := fmt.Sprintf("Review Session %s", msg.SessionID)
	header := slack.NewHeaderBlock(&slack.TextBlockObject{
		Type: slack.PlainTextType,
		Text: title,
	})

	// name and groups metadata
	metaSection1 := slack.NewSectionBlock(nil, []*slack.TextBlockObject{
		{Type: slack.MarkdownType, Text: fmt.Sprintf("name\n*%s*", msg.Name)},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("groups\n*%v*", msg.UserGroups)},
	}, nil)

	// email, session time metadata
	metaSection2 := slack.NewSectionBlock(nil, []*slack.TextBlockObject{
		{Type: slack.MarkdownType, Text: fmt.Sprintf("email\n*%s*", msg.Email)},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("session time\n*%s*", msg.sessionTime())},
	}, nil)

	// connection metadata
	metaSection3 := slack.NewSectionBlock(nil, []*slack.TextBlockObject{
		{Type: slack.MarkdownType, Text: fmt.Sprintf("connection\n*%s*", msg.Connection)},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("type\n*%s*", msg.Type)},
	}, nil)

	// script at the maximum slack allowed size
	scriptBlock := slack.NewSectionBlock(&slack.TextBlockObject{
		Type: slack.MarkdownType,
		Text: fmt.Sprintf("_script_\n```%s```", msg.Script),
	}, nil, nil)
	if msg.SessionTime != nil {
		scriptBlock = slack.NewSectionBlock(&slack.TextBlockObject{Type: slack.PlainTextType, Text: "-"}, nil, nil)
	}

	// URI to the review at portal
	reviewLocation := slack.NewSectionBlock(&slack.TextBlockObject{
		Type: slack.MarkdownType,
		Text: fmt.Sprintf("_Details: <%s|%s>_", msg.WebappURL, msg.ID),
	}, nil, nil)

	blocks := []slack.Block{
		slack.NewDividerBlock(),
		header,
		reviewLocation,
		slack.NewDividerBlock(),

		metaSection1,
		metaSection2,
		metaSection3,

		scriptBlock,

		slack.NewDividerBlock(),
	}

	// add groups button
	for i, groupName := range msg.ApprovalGroups {
		key := fmt.Sprintf("%s:%s", msg.ID, groupName)
		blockID := fmt.Sprintf("%s:%s", key, strconv.Itoa(i))

		blocks = append(blocks,
			slack.NewSectionBlock(&slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: fmt.Sprintf("*Group:* %s", groupName),
			}, nil, nil),
			slack.NewActionBlock(
				blockID,
				slack.NewButtonBlockElement("review-approved", key,
					&slack.TextBlockObject{Type: slack.PlainTextType, Text: "Approve"}).
					WithStyle(slack.StylePrimary),
				slack.NewButtonBlockElement("review-rejected", key,
					&slack.TextBlockObject{Type: slack.PlainTextType, Text: "Reject"}).
					WithStyle(slack.StyleDanger),
			),
		)
	}
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()
	eventKind := EventKindReview
	if msg.SessionTime != nil {
		eventKind = EventKindJit
	}
	metadata := slack.MsgOptionMetadata(slack.SlackMetadata{
		EventType: eventKind,
		EventPayload: map[string]any{
			reviewIDMetadataKey:  msg.ID,
			sessionIDMetadataKey: msg.SessionID,
		},
	})
	_, _, err := s.apiClient.PostMessageContext(ctx, s.slackChannel, slack.MsgOptionBlocks(blocks...), metadata)
	if err != nil {
		return fmt.Errorf("failed sending message to slack channel %v, reason=%v", s.slackChannel, err)
	}
	return nil
}

func (s *SlackService) UpdateMessage(msg *MessageReviewResponse, isApproved bool) error {
	blockID := msg.item.ActionCallback.BlockActions[0].BlockID
	blocks := msg.item.Message.Blocks.BlockSet
	for i, b := range blocks {
		if b.BlockType() == "actions" {
			bl := b.(*slack.ActionBlock)
			if bl.BlockID == blockID {
				blocks[i] = slack.NewSectionBlock(&slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: fmt.Sprintf("@%s `%s` this session at _%v_",
						msg.item.User.Name, msg.Status, time.Now().UTC().Format(time.RFC1123)),
				}, nil, nil)
			}
		}
	}

	if isApproved {
		text := "*Session ready to be executed!*\n"
		if msg.EventKind == EventKindJit {
			text = "*Interactive session ready!*\n"
		}
		blocks = append(blocks,
			slack.NewDividerBlock(),
			slack.NewSectionBlock(&slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: text,
			}, nil, nil))
	}

	_, _, err := s.apiClient.PostMessage(msg.item.Channel.ID,
		slack.MsgOptionReplaceOriginal(msg.item.ResponseURL),
		slack.MsgOptionBlocks(blocks...))
	return err
}

func (s *SlackService) OpenModalError(msg *MessageReviewResponse, message string) error {
	_, err := s.apiClient.OpenView(msg.item.TriggerID, slack.ModalViewRequest{
		ClearOnClose: true,
		Type:         slack.VTModal,
		Title: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Error",
		},
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				slack.NewSectionBlock(&slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: fmt.Sprintf("*%s*", strings.ToUpper(message)),
				}, nil, nil),
			},
		},
	})
	return err
}

func (s *SlackService) UpdateMessageStatus(msg *MessageReviewResponse, message string) error {
	blockID := msg.item.ActionCallback.BlockActions[0].BlockID
	blocks := msg.item.Message.Blocks.BlockSet
	for i, b := range blocks {
		if b.BlockType() == "actions" {
			bl := b.(*slack.ActionBlock)
			if bl.BlockID == blockID {
				blocks[i] = slack.NewSectionBlock(&slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: message,
				}, nil, nil)
			}
		}
	}
	_, _, err := s.apiClient.PostMessage(msg.item.Channel.ID,
		slack.MsgOptionReplaceOriginal(msg.item.ResponseURL),
		slack.MsgOptionBlocks(blocks...))
	return err
}
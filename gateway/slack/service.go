package slack

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

type SlackService struct {
	apiClient     *slack.Client
	socketClient  *socketmode.Client
	slackChannel  string
	slackBotToken string
	instanceID    string
	apiURL        string
	ctx           context.Context
	cancelFn      context.CancelFunc
}

const (
	reviewIDMetadataKey  = "review_id"
	sessionIDMetadataKey = "session_id"
	EventKindOneTime     = "onetime"
	EventKindJit         = "jit"
	// it's usually 2000, keep a more safe number
	maxLabelSize  = 1800
	maxGroupsSize = 50
)

func New(slackBotToken, slackAppToken, slackChannel, instanceID, apiURL string) (*SlackService, error) {
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
		apiClient:     apiClient,
		socketClient:  socketClient,
		slackChannel:  slackChannel,
		slackBotToken: slackBotToken,
		instanceID:    instanceID,
		apiURL:        apiURL,
		ctx:           ctx,
		cancelFn:      cancelFn,
	}, nil

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
	ConnectionType string
	Script         string
	SessionTime    *time.Duration
	WebappURL      string
	SessionID      string
	SlackChannels  []string
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

func (s *SlackService) SendMessageReview(msg *MessageReviewRequest) (result string) {
	title := "Hoop Review"

	header := slack.NewHeaderBlock(&slack.TextBlockObject{
		Type: slack.PlainTextType,
		Text: title,
	})

	groupList := strings.Join(msg.UserGroups, ", ")
	if len(groupList) > maxGroupsSize {
		groupList = groupList[:maxGroupsSize] + " ..."
	}
	// name and groups metadata
	metaSection1 := slack.NewSectionBlock(nil, []*slack.TextBlockObject{
		{Type: slack.MarkdownType, Text: fmt.Sprintf("*Name:* %s", msg.Name)},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("*Groups:* %s", groupList)},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("*Email:* %s", msg.Email)},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("*Session time:* %s", msg.sessionTime())},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("*Connection:* %s", msg.Connection)},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("*Type:* %s", msg.ConnectionType)},
		{Type: slack.MarkdownType, Text: fmt.Sprintf("*<%s|More details>*", msg.WebappURL)},
	}, nil)

	script := msg.Script
	if len(script) > maxLabelSize {
		script = script[:maxLabelSize] + " ..."
	}

	blocks := []slack.Block{
		header,
		metaSection1,
	}
	// script at the maximum slack allowed size
	if script != "" {
		scriptBlock := slack.NewSectionBlock(&slack.TextBlockObject{
			Type: slack.MarkdownType,
			Text: fmt.Sprintf("_script_\n```%s```", script),
		}, nil, nil)
		blocks = append(blocks, scriptBlock)
	}

	// add groups button
	for i, groupName := range msg.ApprovalGroups {
		key := fmt.Sprintf("%s:%s", msg.ID, groupName)
		blockID := fmt.Sprintf("%s:%s", key, strconv.Itoa(i))

		blocks = append(blocks,
			slack.NewSectionBlock(&slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: fmt.Sprintf("*Approver groups:* %s", groupName),
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

	eventKind := EventKindOneTime
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

	slackChannels := msg.SlackChannels
	if s.slackChannel != "" && !slices.Contains(slackChannels, s.slackChannel) {
		slackChannels = append(slackChannels, s.slackChannel)
	}

	var errs []string
	for _, slackChannel := range slackChannels {
		_, _, err := s.apiClient.PostMessage(slackChannel, slack.MsgOptionBlocks(blocks...), metadata)
		if err != nil {
			errs = append(errs, fmt.Sprintf(`"%v - %v"`, slackChannel, err))
		}

		// Slack allows 1 post message per second. reference: https://api.slack.com/apis/rate-limits
		time.Sleep(time.Millisecond * 1200)
	}
	return fmt.Sprintf("success sent channels %v/%v, errors=%v", len(slackChannels), len(slackChannels)-len(errs), errs)
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

// PostMessage sends a message to a channel or direct message to a user
func (s *SlackService) PostMessage(slackOrChannelID, message string) error {
	_, timestamp, err := s.apiClient.PostMessage(slackOrChannelID, slack.MsgOptionText(message, false))
	if err != nil {
		log.Warnf("failed post message to %q at %v, err=%v", slackOrChannelID, timestamp, err)
		return err
	}

	log.Infof("message successfully sent to %q at %s", slackOrChannelID, timestamp)
	return nil
}

func (s *SlackService) PostEphemeralMessage(msg *MessageReviewResponse, message string, msgArgs ...any) error {
	channelID := msg.item.Channel.ID
	userID := msg.item.User.ID

	msgOption := slack.MsgOptionText(fmt.Sprintf(message, msgArgs...), false)
	timestamp, err := s.apiClient.PostEphemeral(channelID, userID, msgOption)
	if err != nil {
		log.Errorf("failed post ephemeral message to the user %s on the channel %s at %v, err=%v", userID, channelID, timestamp, err)
		return err
	}

	log.Infof("message successfully sent to the user %s on the channel %s at %s", userID, channelID, timestamp)
	return nil
}

package slack

import (
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// ProcessEvents start the websocket connection and process the events
// in a go routine. It's a blocking method
func (s *SlackService) ProcessEvents(respCh chan *MessageReviewResponse) error {
	go func() {
		s.processEvents(respCh)
	}()
	return s.socketClient.RunContext(s.ctx)
}

func (s *SlackService) processEvents(respCh chan *MessageReviewResponse) {
	log := log.With("org", s.instanceID)
	for evt := range s.socketClient.Events {
		switch evt.Type {
		case socketmode.EventTypeConnecting:
			log.Info("connecting to Slack with Socket Mode...")
		case socketmode.EventTypeConnectionError:
			log.Info("connection failed. Retrying later...")
		case socketmode.EventTypeConnected:
			log.Info("connected to Slack with Socket Mode")
		case socketmode.EventTypeInteractive:
			s.processInteractive(respCh, evt)
		case socketmode.EventTypeSlashCommand:
			s.processSlashCommandRequest(evt)
		case socketmode.EventTypeHello:
			log.Info("socket live, received ping from slack")
		case socketmode.EventTypeIncomingError:
			eventErr, _ := evt.Data.(*slack.IncomingEventError)
			log.Warnf("received incoming_error from slack, err=%v", eventErr)
		default:
			log.Errorf("event not implemented %s", evt.Type)
		}
	}
}

func (s *SlackService) processInteractive(respCh chan *MessageReviewResponse, ev socketmode.Event) {
	cb, ok := ev.Data.(slack.InteractionCallback)
	if !ok {
		log.Debugf("ignored %+v\n", ev)
		return
	}
	log.Infof("received interaction, user=%v, domain=%s, metaevent=%s",
		cb.User.ID, cb.Team.Domain, cb.Message.Metadata.EventType)

	switch cb.Type {
	case slack.InteractionTypeBlockActions:
		// See https://api.slack.com/apis/connections/socket-implement#button
		reviewResponse := MessageReviewResponse{
			EventKind: fmt.Sprintf("%v", cb.Message.Metadata.EventType),
			ID:        fmt.Sprintf("%v", cb.Message.Metadata.EventPayload[reviewIDMetadataKey]),
			SessionID: fmt.Sprintf("%v", cb.Message.Metadata.EventPayload[sessionIDMetadataKey]),
			Status:    "rejected",
			SlackID:   cb.User.ID,
			item:      cb,
		}
		if cb.ActionCallback.BlockActions[0].ActionID == "review-approved" {
			reviewResponse.Status = "approved"
		}
		// reviewUID:GroupName:IndexNo
		blockID := cb.ActionCallback.BlockActions[0].BlockID
		if parts := strings.Split(blockID, ":"); len(parts) == 3 {
			reviewResponse.GroupName = parts[1]
		}
		select {
		case respCh <- &reviewResponse:
		case <-time.After(time.Second * 2):
			log.Warnf("timeout (2s) on sending review response, id=%v, status=%v",
				reviewResponse.ID, reviewResponse.Status)
		}
	default:
	}
	log.Info("sending ack back to slack!")
	var ack any
	s.socketClient.Ack(*ev.Request, ack)
}

func (s *SlackService) processSlashCommandRequest(ev socketmode.Event) {
	cmd, ok := ev.Data.(slack.SlashCommand)
	if !ok {
		fmt.Printf("Ignored %+v\n", ev)
		return
	}

	log.Infof("received slash command, slackid=%v, domain=%s, command=%v",
		cmd.UserID, cmd.TeamDomain, cmd.Command)

	message := fmt.Sprintf("Please click on this link to integrate your slack user with your user hoop.\n"+
		"%s/slack/user/new/%s", s.apiURL, cmd.UserID)

	_, _, err := s.apiClient.PostMessage(cmd.UserID, slack.MsgOptionText(message, false))
	if err != nil {
		log.Warnf("failed sending slash command response, err=%v", err)
	}

	s.socketClient.Ack(*ev.Request, nil)
}

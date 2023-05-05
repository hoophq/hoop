package slack

import (
	"fmt"
	"strings"
	"time"

	"github.com/runopsio/hoop/common/log"
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
	for evt := range s.socketClient.Events {
		switch evt.Type {
		case socketmode.EventTypeConnecting:
			log.Debugf("connecting to Slack with Socket Mode...")
		case socketmode.EventTypeConnectionError:
			log.Debugf("connection failed. Retrying later...")
		case socketmode.EventTypeConnected:
			log.Debugf("connected to Slack with Socket Mode")
		case socketmode.EventTypeInteractive:
			cb, ok := evt.Data.(slack.InteractionCallback)
			if !ok {
				log.Debugf("ignored %+v\n", evt)
				continue
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
			log.Debugf("sending ack back to slack!")
			var ack any
			s.socketClient.Ack(*evt.Request, ack)
		case socketmode.EventTypeHello:
			log.Debugf("socket live, received ping from slack")
		default:
			log.Warnf("event not implemented %s", evt.Type)
		}
	}
}

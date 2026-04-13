package slack

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// rejectModalMetadata holds the review context serialized into a Slack modal's private_metadata.
type rejectModalMetadata struct {
	ReviewID  string `json:"review_id"`
	SessionID string `json:"session_id"`
	EventKind string `json:"event_kind"`
	GroupName string `json:"group_name"`
	SlackID   string `json:"slack_id"`
	SlackName string `json:"slack_name"`
}

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
	log.Infof("received interaction, user=%v, domain=%s, type=%s",
		cb.User.ID, cb.Team.Domain, cb.Type)

	switch cb.Type {
	case slack.InteractionTypeBlockActions:
		// See https://api.slack.com/apis/connections/socket-implement#button
		actionID := cb.ActionCallback.BlockActions[0].ActionID

		// reviewUID:GroupName:IndexNo
		blockID := cb.ActionCallback.BlockActions[0].BlockID
		groupName := ""
		if parts := strings.Split(blockID, ":"); len(parts) == 3 {
			groupName = parts[1]
		}

		if actionID == "review-rejected" {
			// Persist the block-action callback so the view submission handler can use it
			// as the item — preserving channel, message blocks, responseURL, etc.
			reviewID := fmt.Sprintf("%v", cb.Message.Metadata.EventPayload[reviewIDMetadataKey])
			s.pendingRejectMu.Lock()
			s.pendingRejectItems[reviewID] = cb
			s.pendingRejectMu.Unlock()

			meta := rejectModalMetadata{
				ReviewID:  reviewID,
				SessionID: fmt.Sprintf("%v", cb.Message.Metadata.EventPayload[sessionIDMetadataKey]),
				EventKind: fmt.Sprintf("%v", cb.Message.Metadata.EventType),
				GroupName: groupName,
				SlackID:   cb.User.ID,
				SlackName: cb.User.Name,
			}
			metaJSON, err := json.Marshal(meta)
			// OpenRejectModal must be called before Ack() — TriggerID expires ~3s after interaction.
			if err != nil {
				log.Warnf("failed to serialize reject modal metadata: %v", err)
			} else if err := s.OpenRejectModal(cb, string(metaJSON)); err != nil {
				log.Warnf("failed to open reject modal: %v", err)
			}
			log.Info("sending ack back to slack (reject modal opened)!")
			s.socketClient.Ack(*ev.Request, nil)
			return
		}

		// Approved path — send directly to the processing channel.
		reviewResponse := MessageReviewResponse{
			EventKind: fmt.Sprintf("%v", cb.Message.Metadata.EventType),
			ID:        fmt.Sprintf("%v", cb.Message.Metadata.EventPayload[reviewIDMetadataKey]),
			SessionID: fmt.Sprintf("%v", cb.Message.Metadata.EventPayload[sessionIDMetadataKey]),
			Status:    "approved",
			SlackID:   cb.User.ID,
			GroupName: groupName,
			item:      cb,
		}
		select {
		case respCh <- &reviewResponse:
		case <-time.After(time.Second * 2):
			log.Warnf("timeout (2s) on sending review response, id=%v, status=%v",
				reviewResponse.ID, reviewResponse.Status)
		}

	case slack.InteractionTypeViewSubmission:
		if cb.View.CallbackID != "reject-details-modal" {
			break
		}
		var meta rejectModalMetadata
		if err := json.Unmarshal([]byte(cb.View.PrivateMetadata), &meta); err != nil {
			log.Warnf("failed to parse reject modal metadata: %v", err)
			s.socketClient.Ack(*ev.Request, nil)
			return
		}
		reason := ""
		if block, ok := cb.View.State.Values["rejection_reason_block"]; ok {
			if elem, ok := block["rejection_reason"]; ok {
				reason = elem.Value
			}
		}
		addUsername := false
		if block, ok := cb.View.State.Values["add_username_block"]; ok {
			if elem, ok := block["add_username"]; ok && len(elem.SelectedOptions) > 0 {
				addUsername = true
			}
		}
		if addUsername && meta.SlackName != "" {
			if reason != "" {
				reason = reason + "\nRejected by " + meta.SlackName
			} else {
				reason = "Rejected by " + meta.SlackName
			}
		}
		// Retrieve the original block-action callback so UpdateMessage has the channel,
		// message blocks, and responseURL — making reject behave identically to approve.
		s.pendingRejectMu.Lock()
		originalCb, hasPending := s.pendingRejectItems[meta.ReviewID]
		if hasPending {
			delete(s.pendingRejectItems, meta.ReviewID)
		}
		s.pendingRejectMu.Unlock()

		item := cb
		if hasPending {
			item = originalCb
		}

		reviewResponse := MessageReviewResponse{
			EventKind:       meta.EventKind,
			ID:              meta.ReviewID,
			SessionID:       meta.SessionID,
			Status:          "rejected",
			SlackID:         meta.SlackID,
			GroupName:       meta.GroupName,
			RejectionReason: reason,
			item:            item,
		}
		select {
		case respCh <- &reviewResponse:
		case <-time.After(time.Second * 2):
			log.Warnf("timeout (2s) on sending rejection review response, id=%v", reviewResponse.ID)
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

	message := fmt.Sprintf("Visit the link to associate your Slack user with Hoop.\n"+
		"%s/slack/user/new/%s", s.apiURL, cmd.UserID)

	_, _, err := s.apiClient.PostMessage(cmd.UserID, slack.MsgOptionText(message, false))
	if err != nil {
		log.Warnf("failed sending slash command response, err=%v", err)
	}

	s.socketClient.Ack(*ev.Request, nil)
}

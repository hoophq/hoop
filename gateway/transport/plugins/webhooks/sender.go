package webhooks

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/gateway/appconfig"
	svix "github.com/svix/svix-webhooks/go"
)

var (
	orgStore   = memory.New()
	svixClient *svix.Svix
)

func SendMessage(orgID, eventType string, payload map[string]any) error {
	// client initialization
	if svixClient == nil {
		webhookAppKey := appconfig.Get().WebhookAppKey()
		if webhookAppKey == "" {
			log.With("appid", orgID, "eventtype", eventType).Debugf("svix client was not initialized because it's not set")
			return nil
		}
		webhookAppUrl := appconfig.Get().WebhookAppURL()
		svixClient = svix.New(webhookAppKey, &svix.SvixOptions{ServerUrl: webhookAppUrl})
		log.With("appid", orgID).Infof("webhook client initialized for %v", webhookAppUrl)
	}

	// application setup on Svix using the organization id
	if !orgStore.Has(orgID) {
		log.With("appid", orgID, "eventtype", eventType).Infof("creating new svix app")
		ctxtimeout, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
		defer cancelFn()
		shortOrgID := orgID[0:8]
		out, err := svixClient.Application.GetOrCreate(ctxtimeout, &svix.ApplicationIn{
			Name: shortOrgID,
			Uid:  *svix.NullableString(&orgID),
		})
		if err != nil {
			return fmt.Errorf("failed creating webhook plugin application, reason=%v", err)
		}
		log.With("appid", orgID, "eventtype", eventType).Infof("application created with success, id=%s, uid=%v",
			orgID, out.Uid)
		orgStore.Set(orgID, nil)
	}

	// handle sending messages
	appID := orgID
	eventID := uuid.NewString()
	ctxtimeout, cancelFn := context.WithTimeout(context.Background(), time.Second*3)
	defer cancelFn()
	out, err := svixClient.Message.Create(ctxtimeout, appID, &svix.MessageIn{
		EventType: eventType,
		EventId:   *svix.NullableString(ptr.String(eventID)),
		Payload:   payload,
	})
	if err != nil {
		return fmt.Errorf("failed sending webhook event to remote source, event=%s, err=%v",
			eventType, err)
	}

	if out != nil {
		log.With("appid", appID).Infof("sent webhook with success, id=%s, event=%s, eventid=%s",
			out.Id, out.EventType, eventID)
	}
	return nil
}

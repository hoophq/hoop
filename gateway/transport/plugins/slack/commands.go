package slack

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/security/idp"
	"github.com/hoophq/hoop/gateway/storagev2"
	"golang.org/x/oauth2"
)

type eventCallback struct {
	orgID       string
	ctx         *storagev2.Context
	idpProvider *idp.Provider
}

func (c *eventCallback) CommandSlackSubscribe(command, slackID string) (string, error) {
	log.Infof("received slash command request, org=%s, command=%s, slackid=%s", c.orgID, command, slackID)
	stateUID := uuid.NewString()
	err := models.CreateLogin(&models.Login{
		ID:        stateUID,
		SlackID:   slackID,
		UpdatedAt: time.Now().UTC(),
		Redirect:  fmt.Sprintf("%s/auth/callback", c.idpProvider.ApiURL),
		Outcome:   "",
	})
	if err != nil {
		return "", err
	}
	if c.idpProvider.Audience != "" {
		params := []oauth2.AuthCodeOption{
			oauth2.SetAuthURLParam("audience", c.idpProvider.Audience),
		}
		return c.idpProvider.AuthCodeURL(stateUID, params...), nil
	}

	return c.idpProvider.AuthCodeURL(stateUID), nil
}

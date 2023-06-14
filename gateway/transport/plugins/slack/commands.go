package slack

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/storagev2/user"
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
	err := user.UpdateLoginState(c.ctx, &types.Login{
		ID:      stateUID,
		SlackID: slackID,
		// redirect to webapp
		Redirect: fmt.Sprintf("%s/auth/callback", c.idpProvider.ApiURL),
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

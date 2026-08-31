// Package usertoken checks the access token stored for a session user, on a
// timer and at proxy session start. It refuses or ends a session that fails.
package usertoken

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
)

const pollingInterval = 5 * time.Minute

// Test seams: the real ones need models.DB and a live identity provider.
var (
	loadUserToken = func(userID string) (*models.UserToken, error) {
		return models.GetUserToken(models.DB, userID)
	}
	idpRefreshExpiredToken = idp.TryRefreshExpiredToken
)

// CheckUserToken may refresh: it makes a network call and writes a new token.
func CheckUserToken(tokenVerifier idp.UserInfoTokenVerifier, userID string) error {
	userToken, err := loadUserToken(userID)
	if err != nil {
		return err
	}
	if userToken == nil {
		return fmt.Errorf("access token not found for user subject")
	}

	uinfo, err := tokenVerifier.VerifyAccessToken(userToken.Token)
	if err != nil {
		// local and SAML flatten the JWT error (common/keys/keys.go), so only
		// the text is common to them and the OIDC JWT path.
		if strings.Contains(err.Error(), "token is expired") {
			if err := refreshSessionToken(tokenVerifier, userID, userToken.Token); err != nil {
				// The Postgres proxy and the CLI show this string to the user,
				// so the wording stays as it was. The reason goes to the log.
				log.With("subject", userID).Warnf("failed to refresh the expired access token: %v", err)
				return fmt.Errorf("access token is expired, try logging in again")
			}
			return nil
		}

		return err
	}

	if uinfo == "" {
		return fmt.Errorf("user subject not found using the access token")
	}

	return nil
}

// refreshSessionToken exchanges the stored refresh token. Only the OIDC
// provider implements that, so a local or SAML session ends here.
func refreshSessionToken(tokenVerifier idp.UserInfoTokenVerifier, userID, expiredToken string) error {
	subject, newAccessToken, err := idpRefreshExpiredToken(tokenVerifier, expiredToken)
	if err != nil {
		return err
	}
	// The database keys the token by subject. A different subject means the
	// refresh got the token of another user, so this session ends.
	if subject != userID {
		return fmt.Errorf("refreshed subject does not match the session subject")
	}
	// A good exchange does not prove the new token verifies.
	newSubject, err := tokenVerifier.VerifyAccessToken(newAccessToken)
	if err != nil {
		return fmt.Errorf("the refreshed access token does not verify: %w", err)
	}
	if newSubject != userID {
		return fmt.Errorf("the refreshed access token is not for the session subject")
	}

	log.With("subject", userID).Infof("access token refreshed, the session continues")
	return nil
}

// PollingUserToken calls cancel with the check error when the token is not
// usable. ctx must end with the session, or the poller outlives it.
func PollingUserToken(ctx context.Context, cancel context.CancelCauseFunc, tokenVerifier idp.UserInfoTokenVerifier, userID string) {
	pollUserToken(ctx, cancel, tokenVerifier, userID, pollingInterval)
}

// pollUserToken takes the interval and returns a stop channel, which
// PollingUserToken hides from the API. Only tests need them.
func pollUserToken(ctx context.Context, cancel context.CancelCauseFunc, tokenVerifier idp.UserInfoTokenVerifier, userID string, interval time.Duration) <-chan struct{} {
	stopped := make(chan struct{})
	// A nil Done channel means the context can never cancel, so the poller
	// would outlive the session and keep refreshing a departed user's token.
	if ctx.Done() == nil {
		err := fmt.Errorf("user token poller got a context that never cancels")
		log.With("subject", userID).Error(err.Error())
		cancel(err)
		close(stopped)
		return stopped
	}
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := CheckUserToken(tokenVerifier, userID)
				if err != nil {
					log.Errorf("Error verifying the user token: %v", err)
					cancel(err)
					return
				}
			}
		}
	}()
	return stopped
}

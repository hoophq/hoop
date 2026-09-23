package usertoken

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

const (
	pollingInterval = 5 * time.Minute
	// Mirrors streamclient.proxyMaxTimeoutDuration.
	maxSessionAge = 48 * time.Hour
)

var (
	getUserContext = models.GetUserContext
	getUserToken   = func(userID string) (*models.UserToken, error) {
		return models.GetUserToken(models.DB, userID)
	}
	refreshExpiredToken = idp.TryRefreshExpiredToken
)

// CheckUserToken reports whether userID may hold a session. An expired
// access token is refreshed; the session ends if the refresh fails.
// It may call the IdP and write a new token.
func CheckUserToken(tokenVerifier idp.UserInfoTokenVerifier, userID string) error {
	userCtx, err := getUserContext(userID)
	if err != nil {
		return err
	}
	if userCtx.IsEmpty() {
		return fmt.Errorf("user not found")
	}
	if userCtx.UserStatus != string(types.UserStatusActive) {
		return fmt.Errorf("user is not active")
	}

	userToken, err := getUserToken(userID)
	if err != nil {
		return err
	}
	if userToken == nil {
		return fmt.Errorf("access token not found for user subject")
	}

	subject, err := tokenVerifier.VerifyAccessToken(userToken.Token)
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		if err := refreshSessionToken(tokenVerifier, userID, userToken.Token); err != nil {
			// The CLI and the Postgres proxy show this text to the user.
			log.With("subject", userID).Warnf("failed to refresh the expired access token: %v", err)
			return fmt.Errorf("access token is expired, try logging in again")
		}
		return nil
	case err != nil:
		return err
	case subject == "":
		return fmt.Errorf("user subject not found using the access token")
	}
	return nil
}

// refreshSessionToken exchanges the stored refresh token. Only OIDC
// supports it, so local and SAML sessions end at token expiry.
func refreshSessionToken(tokenVerifier idp.UserInfoTokenVerifier, userID, expiredToken string) error {
	subject, newAccessToken, err := refreshExpiredToken(tokenVerifier, expiredToken)
	if err != nil {
		return err
	}
	// Tokens are stored by subject: a mismatch means another user's token.
	if subject != userID {
		return fmt.Errorf("refreshed subject does not match the session subject")
	}
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

// PollingUserToken ends the session when the user check fails or the
// session reaches maxSessionAge. ctx must end with the session.
func PollingUserToken(ctx context.Context, cancel context.CancelCauseFunc, tokenVerifier idp.UserInfoTokenVerifier, userID string) {
	pollUserToken(ctx, cancel, tokenVerifier, userID, pollingInterval, maxSessionAge)
}

func pollUserToken(ctx context.Context, cancel context.CancelCauseFunc, tokenVerifier idp.UserInfoTokenVerifier, userID string, interval, maxAge time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if ctx.Done() == nil {
		err := fmt.Errorf("user token poller requires a cancellable context")
		log.With("subject", userID).Error(err)
		cancel(err)
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		maxAgeTimer := time.NewTimer(maxAge)
		defer maxAgeTimer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-maxAgeTimer.C:
				cancel(fmt.Errorf("session reached the maximum duration (%s)", maxAge))
				return
			case <-ticker.C:
				if err := CheckUserToken(tokenVerifier, userID); err != nil {
					log.With("subject", userID).Warnf("ending session: %v", err)
					cancel(err)
					return
				}
			}
		}
	}()
	return done
}

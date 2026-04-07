package transport

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

func CheckUserToken(tokenVerifier idp.UserInfoTokenVerifier, userID string) error {
	userToken, err := models.GetUserToken(models.DB, userID)
	if err != nil {
		return err
	}
	if userToken == nil {
		return fmt.Errorf("access token not found for user subject")
	}

	uinfo, err := tokenVerifier.VerifyAccessToken(userToken.Token)
	if err != nil {
		if !strings.Contains(err.Error(), "token is expired") {
			return err
		}

		// attempt to refresh the token if a refresh token is available
		if userToken.RefreshToken == nil || *userToken.RefreshToken == "" {
			return fmt.Errorf("access token is expired, try logging in again")
		}

		refresher, ok := tokenVerifier.(idp.TokenRefresher)
		if !ok {
			return fmt.Errorf("access token is expired, try logging in again")
		}

		newToken, refreshErr := refresher.RefreshAccessToken(context.Background(), *userToken.RefreshToken)
		if refreshErr != nil {
			log.With("user", userID).Warnf("failed to refresh access token: %v", refreshErr)
			return fmt.Errorf("access token is expired, try logging in again")
		}

		var newRefreshToken *string
		if newToken.RefreshToken != "" {
			newRefreshToken = &newToken.RefreshToken
		}
		if err := models.UpsertUserToken(models.DB, userID, newToken.AccessToken, newRefreshToken); err != nil {
			log.With("user", userID).Errorf("failed to persist refreshed token: %v", err)
			return fmt.Errorf("access token is expired, try logging in again")
		}

		log.With("user", userID).Infof("access token refreshed successfully")
		return nil
	}

	if uinfo == "" {
		return fmt.Errorf("user subject not found using the access token")
	}

	return nil
}

func PollingUserToken(ctx context.Context, cancel context.CancelCauseFunc, tokenVerifier idp.UserInfoTokenVerifier, userID string) {
	go func() {
		ticker := time.NewTicker(pollingInterval)
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
}

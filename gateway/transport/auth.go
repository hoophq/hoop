package transport

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/gateway/idp"
)

const pollingInterval = 5 * time.Minute

func CheckUserToken(tokenVerifier idp.UserInfoTokenVerifier, userID string) error {
	token, ok := idp.UserTokens.Load(userID)
	if !ok || token == "" {
		return fmt.Errorf("access token not found for user subject")
	}

	tokenStr, _ := token.(string)
	uinfo, err := tokenVerifier.VerifyAccessToken(tokenStr)
	if err != nil {
		if strings.Contains(err.Error(), "token is expired") {
			return fmt.Errorf("access token is expired, try logging in again")
		}

		return err
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
				token, ok := idp.UserTokens.Load(userID)
				if !ok || token == "" {
					cancel(fmt.Errorf("access token not found for user"))
					return
				}

				err := CheckUserToken(tokenVerifier, userID)
				if err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
}

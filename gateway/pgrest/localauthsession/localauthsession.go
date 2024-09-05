package pglocalauthsession

import (
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/pgrest"
)

func CreateSession(authSession pgrest.LocalAuthSession) (string, error) {
	if authSession.ID == "" {
		authSession.ID = uuid.New().String()
	}
	return authSession.ID, pgrest.New("/local_auth_sessions").Create(authSession).Error()
}

func GetSessionByToken(sessionToken string) (*pgrest.LocalAuthSession, error) {
	var sess pgrest.LocalAuthSession
	err := pgrest.New("/local_auth_sessions?token=eq.%s", sessionToken).
		FetchOne().
		DecodeInto(&sess)
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

package pgfavoritesession

import (
	"github.com/hoophq/hoop/gateway/pgrest"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

func Upsert(favoriteSession types.FavoriteSession) (err error) {
	err = pgrest.New("/favoritesessions").Upsert(map[string]any{
		"id":            favoriteSession.ID,
		"session_id":    favoriteSession.SessionID,
		"script":        favoriteSession.Script,
		"labels":        favoriteSession.Labels,
		"user_email":    favoriteSession.UserEmail,
		"user_id":       favoriteSession.UserID,
		"user_name":     favoriteSession.UserName,
		"type":          favoriteSession.Type,
		"connection":    favoriteSession.Connection,
		"verb":          favoriteSession.Verb,
		"event_size":    favoriteSession.EventSize,
		"start_session": favoriteSession.StartSession,
		"end_session":   favoriteSession.EndSession,
	}).Error()

	return err
}

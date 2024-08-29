package sessionapi

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"github.com/hoophq/hoop/gateway/storagev2"
	sessionstorage "github.com/hoophq/hoop/gateway/storagev2/session"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

// Post a new favorite session
func AddFavorite(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log := pgusers.ContextLogger(c)

	sessionID := c.Param("session_id")
	session, err := sessionstorage.FindOne(ctx, sessionID)
	if err != nil {
		log.Errorf("failed fetching session, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching session"})
		return
	}
	if session == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	favoritesessionID := uuid.NewString()
	var favoritesession = types.FavoriteSession{
		ID:           favoritesessionID,
		SessionID:    session.ID,
		OrgID:        session.OrgID,
		Script:       session.Script,
		Labels:       session.Labels,
		UserEmail:    session.UserEmail,
		UserID:       session.UserID,
		UserName:     session.UserName,
		Type:         session.Type,
		Connection:   session.Connection,
		Verb:         session.Verb,
		EventSize:    session.EventSize,
		StartSession: session.StartSession,
		EndSession:   session.EndSession,
	}

	c.PureJSON(http.StatusOK, favoritesession)
}

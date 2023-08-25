package sessionapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
	connectionstorage "github.com/runopsio/hoop/gateway/storagev2/connection"
	sessionStorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

type SessionPostBody struct {
	Script     string              `json:"script"`
	Connection string              `json:"connection"`
	Labels     types.SessionLabels `json:"labels"`
	ClientArgs []string            `json:"client_args"`
}

func Post(c *gin.Context) {
	ctx := user.ContextUser(c)
	storageCtx := storagev2.ParseContext(c)

	var body SessionPostBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	}

	if body.Connection == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "You must provide the connection name"})
		return
	}

	connection, err := connectionstorage.GetOneByName(storageCtx, body.Connection)
	if connection == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Connection not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	newSession := types.Session{
		ID:           uuid.NewString(),
		OrgID:        ctx.Org.Id,
		Labels:       body.Labels,
		Script:       types.SessionScript{"data": body.Script},
		UserEmail:    ctx.User.Email,
		UserID:       ctx.User.Id,
		UserName:     ctx.User.Name,
		Type:         connection.Type,
		Connection:   connection.Name,
		Verb:         pb.ClientVerbExec,
		Status:       types.SessionStatusOpen,
		DlpCount:     0,
		StartSession: time.Now().UTC(),
	}
	log.Debugf("persisting session")

	err = sessionStorage.Put(storageCtx, newSession)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "The session couldn't be created"})
	}

	// running RunExec from run-exec.go
	RunExec(c, newSession, body.ClientArgs)
}

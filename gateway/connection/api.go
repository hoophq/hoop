package connection

import (
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	sessionapi "github.com/runopsio/hoop/gateway/api/session"
	"github.com/runopsio/hoop/gateway/storagev2"
	connectionstorage "github.com/runopsio/hoop/gateway/storagev2/connection"
	sessionStorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Persist(httpMethod string, context *user.Context, c *Connection) (int64, error)
		FindAll(context *user.Context) ([]BaseConnection, error)
		FindOne(context *user.Context, name string) (*Connection, error)
		Evict(ctx *user.Context, connectionName string) error
	}
)

func (a *Handler) FindOne(c *gin.Context) {
	context := user.ContextUser(c)

	name := c.Param("name")
	connection, err := a.Service.FindOne(context, name)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if connection == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.PureJSON(http.StatusOK, connection)
}

func (a *Handler) FindAll(c *gin.Context) {
	context := user.ContextUser(c)

	connections, err := a.Service.FindAll(context)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.PureJSON(http.StatusOK, connections)
}

func (a *Handler) Post(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	var connection Connection
	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingCon, err := a.Service.FindOne(context, connection.Name)
	if err != nil {
		log.Errorf("failed fetching existing connection, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingCon != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "Connection already exists."})
		return
	}

	// TODO: Move this alternative solution to other place
	if connection.Type == Type(pb.ConnectionTypePostgres) {
		connection.Command = []string{"psql", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
	}

	if connection.Type == Type(pb.ConnectionTypeMySQL) {
		connection.Command = []string{"mysql", "-h$HOST", "-u$USER", "--port=$PORT", "-D$DB"}
	}

	_, err = a.Service.Persist("POST", context, &connection)
	if err != nil {
		log.Errorf("failed creating connection, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, connection)
}

func (a *Handler) Put(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	name := c.Param("name")
	existingConnection, err := a.Service.FindOne(context, name)
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if existingConnection == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	var connection Connection
	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	connection.Id = existingConnection.Id

	_, err = a.Service.Persist("PUT", context, &connection)
	if err != nil {
		log.Errorf("failed persisting connection, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, connection)
}

func (a *Handler) Evict(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	connectionName := c.Param("name")
	if connectionName == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "missing connection name"})
		return
	}
	err := a.Service.Evict(ctx, connectionName)
	switch err {
	case errNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
	case nil:
		c.Writer.WriteHeader(204)
	default:
		log.Errorf("failed evicting connection %v, err=%v", connectionName, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed removing connection"})
	}
}

// DEPRECATED in flavor of POST /api/sessions
func (h *Handler) RunExec(c *gin.Context) {
	log.Warnf("executing connection run-exec - deprecated endpoint")
	ctx := user.ContextUser(c)
	storageCtx := storagev2.ParseContext(c)

	// connection attribute is unused here
	var body sessionapi.SessionPostBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	connectionName := c.Param("name")
	connection, err := connectionstorage.GetOneByName(storageCtx, connectionName)
	if connection == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Connection not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	newSession := types.Session{
		ID:         uuid.NewString(),
		OrgID:      ctx.Org.Id,
		Labels:     body.Labels,
		Script:     types.SessionScript{"data": body.Script},
		UserEmail:  ctx.User.Email,
		UserID:     ctx.User.Id,
		UserName:   ctx.User.Name,
		Type:       connection.Type,
		Connection: connection.Name,
		// As this endpoint is exclusive for exec, we're forcing the Verb to be exec
		Verb:         pb.ClientVerbExec,
		Status:       "open", // TODO use a const
		DlpCount:     0,
		StartSession: time.Now().UTC(),
	}

	log.Debugf("persisting session")

	err = sessionStorage.Write(storageCtx, newSession)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "The session couldn't be created"})
	}

	sessionapi.RunExec(c, newSession, body.ClientArgs)
}

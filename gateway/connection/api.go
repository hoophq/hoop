package connection

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/clientexec"
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

func (h *Handler) RunExec(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	var req clientexec.ExecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"session_id": nil,
			"message":    err.Error()})
		return
	}
	connectionName := c.Param("name")
	conn, err := h.Service.FindOne(ctx, connectionName)
	if err != nil {
		log.Errorf("failed retrieving connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, &clientexec.ExecErrResponse{Message: "failed retrieving connection"})
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, &clientexec.ExecErrResponse{Message: "connection not found"})
		return
	}
	client, err := clientexec.New(ctx.Org.Id, getAccessToken(c), connectionName, "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"session_id": nil, "message": err.Error()})
		return
	}
	clientResp := make(chan *clientexec.Response)
	go func() {
		defer close(clientResp)
		defer client.Close()
		select {
		case clientResp <- client.Run([]byte(req.Script), nil):
		default:
		}
	}()
	log.With("session", client.SessionID()).Infof("api exec, connection=%v", connectionName)
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", client.SessionID()))
	statusCode := http.StatusOK
	if req.Redirect {
		statusCode = http.StatusFound
	}
	select {
	case resp := <-clientResp:
		if resp.IsError() {
			c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{
				SessionID: &resp.SessionID,
				Message:   resp.ErrorMessage(),
				ExitCode:  resp.ExitCode,
			})
			return
		}
		c.JSON(statusCode, resp)
	case <-time.After(time.Second * 50):
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()
		c.JSON(http.StatusAccepted, gin.H{"session_id": client.SessionID(), "exit_code": nil})
	}
}

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}

package connection

import (
	"fmt"
	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/grpc"
	pb "github.com/runopsio/hoop/common/proto"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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
		ProcessClientExec(inputPayload []byte, client pb.ClientTransport) (int, error)
	}
)

func (a *Handler) FindOne(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

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
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	connections, err := a.Service.FindAll(context)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.PureJSON(http.StatusOK, connections)
}

func (a *Handler) Post(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	var connection Connection
	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingCon, err := a.Service.FindOne(context, connection.Name)
	if err != nil {
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
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, connection)
}

func (a *Handler) Put(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	name := c.Param("name")
	existingConnection, err := a.Service.FindOne(context, name)
	if err != nil {
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
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, connection)
}

func (h *Handler) RunExec(c *gin.Context) {
	obj, _ := c.Get("context")
	ctx, _ := obj.(*user.Context)
	var req ExecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"session_id": nil,
			"message":    err.Error()})
		return
	}
	connectionName := c.Param("name")
	conn, err := h.Service.FindOne(ctx, connectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &ExecErrResponse{Message: "failed retrieving connection"})
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, &ExecErrResponse{Message: "connection not found"})
		return
	}
	sessionID := uuid.NewString()
	client, err := grpc.Connect("127.0.0.1:8010", getAccessToken(c),
		grpc.WithOption(grpc.OptionConnectionName, connectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClientAPI),
		grpc.WithOption("verb", pb.ClientVerbExec),
		grpc.WithOption("session-id", sessionID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"session_id": nil, "message": err.Error()})
		return
	}
	clientResp := make(chan ExecResponse)
	go func() {
		defer close(clientResp)
		defer client.Close()
		exitCode, err := h.Service.ProcessClientExec([]byte(req.Script), client)
		select {
		case clientResp <- ExecResponse{err, exitCode}:
		default:
		}
	}()

	log.Printf("session=%s - api exec, connection=%s", sessionID, connectionName)
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", sessionID))
	statusCode := http.StatusOK
	if req.Redirect {
		statusCode = http.StatusFound
	}
	select {
	case resp := <-clientResp:
		switch resp.ExitCode {
		case nilExitCode:
			// means the gRPC client returned an error in the client flow
			c.JSON(http.StatusBadRequest, &ExecErrResponse{
				SessionID: &sessionID,
				Message:   fmt.Sprintf("%v", resp.Err)})
		case 0:
			c.JSON(statusCode, gin.H{"session_id": sessionID, "exit_code": 0})
		default:
			c.JSON(http.StatusBadRequest, &ExecErrResponse{
				SessionID: &sessionID,
				Message:   fmt.Sprintf("%v", resp.Err),
				ExitCode:  &resp.ExitCode})
		}
	case <-time.After(time.Second * 50):
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()
		c.JSON(http.StatusAccepted, gin.H{"session_id": sessionID, "exit_code": nil})
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

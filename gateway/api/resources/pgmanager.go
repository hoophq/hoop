package resources

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
	"gorm.io/gorm"
)

var supportedPrivileges = map[string]string{
	"SELECT":     "",
	"INSERT":     "",
	"UPDATE":     "",
	"DELETE":     "",
	"TRUNCATE":   "",
	"REFERENCES": "",
	"TRIGGER":    "",
	"CREATE":     "",
	"EXECUTE":    "",
}

const featureName string = "resource-provisioning-hub"

// ResourceHealthCheck
//
//	@Summary		Tests connectivity for a resource
//	@Description	Performs a connectivity test to see if the resource has network connectivity and the permissions are configured properly.
//	@Tags			Resources
//	@Produces		json
//	@Param			name		path		string	true	"The resource name"
//	@Success		200			{object}	openapi.ResourceConnectResponse
//	@Failure		400,404,422	{object}	openapi.HTTPError
//	@Router			/resources/{name}/health [post]
func ResourceHealthCheck(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	name := c.Param("name")

	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, name, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource: %v", err)
		return
	}
	if resource == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	resp, err := transportsystem.BareExec(
		map[string]string{},
		pbsystem.UserInfo{
			// ID:             uuid.NewString(),
			// OrgID:          ctx.OrgID,
			// Connection:     resource.ConnectionName,
			// ConnectionType: resource.ConnectionType,
			// UserID:         ctx.UserID,
			// UserName:       ctx.UserName,
			// UserEmail:      ctx.UserEmail,
		},
		&pbsystem.BareExecRequest{
			SID:     uuid.NewString(),
			AgentID: resource.AgentID.String,
			Script:  resourceHealthCheckTest(resource.SubType.String),
			Command: resourceManagerCommand(resource.SubType.String),
			EnvVars: map[string]string{
				// "USER":       staticCred["user"],
				// "HOST":       staticCred["host"],
				// "PORT":       staticCred["port"],
				// "PGPASSWORD": staticCred["password"],
			},
			// EnvVars:      resourceManagerEnvVars(resource.Envs),
		},
		// resource.AgentID.String,
		// &pbsystem.ResourceManagerRequest{
		// 	OrgID:        ctx.OrgID,
		// 	UserID:       ctx.UserID,
		// 	UserName:     ctx.UserName,
		// 	UserEmail:    ctx.UserEmail,
		// 	ResourceName: name,
		// 	ResourceType: resource.Type,
		// 	Script:       resourceHealthCheckTest(resource.SubType.String),
		// 	Command:      resourceManagerCommand(resource.SubType.String),
		// 	EnvVars: map[string]string{
		// 		"USER":       "hoopdevuser",
		// 		"HOST":       staticHostIP,
		// 		"PORT":       "5449",
		// 		"PGPASSWORD": "1a2b3c4d",
		// 	},
		// 	// EnvVars:      resourceManagerEnvVars(resource.Envs),
		// },
	)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusBadRequest, err, "failed testing connectivity: %v", err)
		return
	}

	if resp.Status == pbsystem.StatusFailedType {
		c.JSON(http.StatusUnprocessableEntity, openapi.ResourceConnectResponse{
			Output: resp.Output,
			Status: "error",
		})
		return
	}

	c.JSON(http.StatusOK, openapi.ResourceConnectResponse{
		Output: resp.Output,
		Status: "ok",
	})
}

func ResourceHealthCheckBatch(c *gin.Context) {
	// TODO
}

func ResourcePlan(c *gin.Context) {
	ctx, req, err := parseResourcePlanRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	resourceName := c.Param("name")
	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, resourceName, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource: %v", err)
		return
	}
	if resource == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found: " + resourceName})
		return
	}

	roleName := req.Role
	if req.Type == "managed" {
		roleName, err = generateSecurePostgresRoleName(resource.Name, req.Role)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed generating secure postgres role name: %v", err)
			return
		}
	}

	requestPayload, _ := json.MarshalIndent(req, "", "  ")
	sess, err := openSession(roleName, resource.Type, resource.SubType.String, string(requestPayload), ctx)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating session: %v", err)
		return
	}

	resp, err := transportsystem.RunPgManagerPlan(resource.AgentID.String, &pbsystem.PgManagerPlanRequest{
		SID:            uuid.NewString(),
		RoleName:       roleName,
		Type:           req.Type,
		Scopes:         req.Scopes,
		Privileges:     req.Privileges,
		PgCredentials:  staticCred,
		RotatePassword: req.RotatePassword,
	})

	var sessionOutput string
	if err != nil {
		sessionOutput = err.Error()
	}

	if resp != nil {
		switch {
		case resp.Message != "":
			sessionOutput = resp.Message
		case len(resp.StateMigration) > 0:
			sessionOutput = string(resp.StateMigration)
		}
	}

	exitCode := 0
	if resp.Status != "success" {
		exitCode = 1
	}

	if err := sess.close(sessionOutput, exitCode); err != nil {
		log.With("sid", sess.SID()).Errorf("failed closing session for pg manager plan: %v", err)
	}

	c.JSON(http.StatusOK, openapi.ResourcePlanResult{
		SID:          sess.SID(),
		ResourceName: resourceName,
		Role:         roleName,
		Status:       resp.Status,
		Message:      resp.Message,
	})
}

// ResourcePlanBatch
//
//	@Summary		Creates a batch resource plan
//	@Description	Validates provisioning plans for a batch of resources by executing a SELECT 1 test query against each target database. Each item in the batch is session-audited and receives its own plan ID.
//	@Tags			Resources
//	@Accept			json
//	@Produces		json
//	@Param			request		body		openapi.ResourcePlanRequest		true	"The request body"
//	@Success		200			{object}	openapi.ResourcePlanResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/resources/plan [post]
func ResourcePlanBatch(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	_ = ctx
}

func ResourceApply(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ResourceApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	resourceName := c.Param("name")
	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, resourceName, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource: %v", err)
		return
	}
	if resource == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found: " + resourceName})
		return
	}

	planSession, err := models.GetSessionByID(ctx.OrgID, req.SID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusNotFound, err, "failed retrieving session: %v", err)
		return
	}
	if planSession.ConnectionSubtype != "postgres" {
		httputils.AbortWithErr(c, http.StatusUnprocessableEntity, err, "resource type not implemented: %q", planSession.ConnectionSubtype)
		return
	}
	// TODO: validate session tags
	// TODO: validate plan expiration date

	blob, err := planSession.GetBlobStream()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed obtaining blob stream: %v", err)
		return
	}
	stateMigration, err := parseEventStreamPlan(blob)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed parsing session plan blob stream: %v", err)
		return
	}
	// TODO: validate tags!

	sess, err := openSession(planSession.Connection, resource.Type, resource.SubType.String, string(stateMigration), ctx)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating session: %v", err)
		return
	}

	resp, err := transportsystem.RunPgManagerApply(resource.AgentID.String, &pbsystem.PgManagerApplyRequest{
		SID:            sess.SID(),
		StateMigration: stateMigration,
		PgCredentials:  staticCred,
	})

	var sessionOutput string
	if err != nil {
		sessionOutput = err.Error()
	}

	if resp != nil {
		switch {
		case resp.Message != "":
			sessionOutput = resp.Message
		case len(resp.StateMigration) > 0:
			sessionOutput = string(resp.StateMigration)
		}
	}

	status := resp.Status
	if status == "success" {
		switch planSession.ConnectionSubtype {
		case "postgres":
			err := syncPostgresResourceRole(ctx, resourceName, resp.RoleName, resource.AgentID.String,
				resp.RoleName, resp.RolePassword)
			if err != nil {
				status = "failed"
				errMsg := fmt.Sprintf("failed updating resource role: %v", err)
				log.With("sid", sess.SID()).Warn(errMsg)
				sessionOutput += "\n\n---\n\n"
				sessionOutput += errMsg
			}
		default:
			status = "failed"
			errMsg := fmt.Sprintf("failed updating resource role, subtype (%q) not implemented",
				planSession.ConnectionSubtype)
			log.With("sid", sess.SID()).Warn(errMsg)
			sessionOutput += "\n\n---\n\n"
			sessionOutput += errMsg
		}
	}

	exitCode := 0
	if status != "success" {
		exitCode = 1
	}

	if err := sess.close(sessionOutput, exitCode); err != nil {
		log.With("sid", sess.SID()).Errorf("failed closing session for pg manager plan: %v", err)
	}

	c.JSON(http.StatusOK, openapi.ResourceApplyResult{
		SID:     resp.SID,
		Status:  status,
		Message: resp.Message,
	})
}

// ResourceApplyBatch
//
//	@Summary		Creates a batch resource plan
//	@Description	Validates provisioning plans for a batch of resources by executing a SELECT 1 test query against each target database. Each item in the batch is session-audited and receives its own plan ID.
//	@Tags			Resources
//	@Accept			json
//	@Produces		json
//	@Param			request		body		openapi.ResourcePlanRequest		true	"The request body"
//	@Success		200			{object}	openapi.ResourcePlanResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/resources/plan [post]
func ResourceApplyBatch(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	_ = ctx
}

// resourceManagerCommand returns the runtime entrypoint for the given database subtype.
func resourceManagerCommand(subType string) []string {
	switch subType {
	case "postgres":
		return []string{"psql", "-v", "ON_ERROR_STOP=1", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "postgres"}
	}
	return nil
}

// resourceHealthCheckTest returns a minimal connectivity-test script for the
// given database subtype — just enough to verify the connection is reachable.
func resourceHealthCheckTest(subType string) string {
	switch subType {
	case "postgres":
		// TODO(san): check for permissions as well, e.g. by attempting to create a temporary table or role.
		return `SELECT 1;`
	}
	return ""
}

func parseResourcePlanRequest(c *gin.Context) (*storagev2.Context, *openapi.ResourcePlanItem, error) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ResourcePlanItem
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, nil, fmt.Errorf("failed parsing request: %v", err)
	}

	var unsupportedPrivileges []string
	for _, priv := range req.Privileges {
		if _, ok := supportedPrivileges[priv]; !ok {
			unsupportedPrivileges = append(unsupportedPrivileges, priv)
		}
	}
	if len(unsupportedPrivileges) > 0 {
		return nil, nil, fmt.Errorf("found unsupported privileges=%v", unsupportedPrivileges)
	}
	return ctx, &req, nil

}

func toEventStream(output string) json.RawMessage {
	encoded := base64.StdEncoding.EncodeToString([]byte(output))
	return json.RawMessage(fmt.Sprintf(`[[0,"o",%q]]`, encoded))
}

func parseEventStreamPlan(blob *models.Blob) ([]byte, error) {
	if blob == nil || len(blob.BlobStream) == 0 {
		return nil, errors.New("empty blob stream")
	}

	var eventStream []any
	if err := json.Unmarshal(blob.BlobStream, &eventStream); err != nil {
		return nil, fmt.Errorf("failed decoding blob stream: %v", err)
	}

	if len(eventStream) == 0 {
		return nil, errors.New("empty event stream")
	}
	eventList := eventStream[0].([]any)
	if len(eventList) != 3 {
		return nil, errors.New("invalid event stream format")
	}

	eventData, _ := base64.StdEncoding.DecodeString(eventList[2].(string))
	return eventData, nil
}

type stateSession struct {
	session *models.Session
}

func openSession(resourceRole, connType, connSubtype, input string, ctx *storagev2.Context) (*stateSession, error) {
	sid := uuid.NewString()
	session := models.Session{
		ID:                sid,
		OrgID:             ctx.OrgID,
		UserID:            ctx.UserID,
		UserName:          ctx.UserName,
		UserEmail:         ctx.UserEmail,
		Connection:        resourceRole, // TODO: fix-me for external type
		ConnectionType:    connType,
		ConnectionSubtype: connSubtype,
		ConnectionTags: map[string]string{
			"hoopdev/managed-by": featureName,
		},
		Verb:      proto.ClientVerbExec,
		Status:    "open",
		BlobInput: models.BlobInputType(input),
		CreatedAt: time.Now().UTC(),
	}

	if err := models.UpsertSession(session); err != nil {
		return nil, fmt.Errorf("failed creating session: %v", err)
	}
	return &stateSession{session: &session}, nil
}

func (s *stateSession) SID() string { return s.session.ID }
func (s *stateSession) close(output string, exitCode int) error {
	endTime := time.Now().UTC()
	return models.UpdateSessionEventStream(models.SessionDone{
		ID:         s.session.ID,
		OrgID:      s.session.OrgID,
		Metrics:    map[string]any{},
		BlobStream: toEventStream(output),
		Status:     "done",
		ExitCode:   &exitCode,
		EndSession: &endTime,
	})
}

func syncPostgresResourceRole(ctx *storagev2.Context, resourceName, resourceRole, agentID, userRole, userRolePwd string) error {
	err := upsertConnection(ctx, &models.Connection{
		OrgID:        ctx.OrgID,
		ID:           uuid.NewString(),
		ResourceName: resourceName,
		AgentID:      sql.NullString{Valid: true, String: agentID},
		Name:         resourceRole,
		Command: []string{
			"psql", "-v",
			"ON_ERROR_STOP=1",
			"-A", "-F\t",
			"-P", "pager=off",
			"-h", "$HOST",
			"-U", "$USER",
			"--port=$PORT", "$DB",
		},
		Type:    "database",
		SubType: sql.NullString{String: "postgres", Valid: true},
		// assume online, because it was be able to run the apply with the agent id
		Status:             models.ConnectionStatusOnline,
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "enabled",
		Envs: map[string]string{
			"envvar:HOST": b64enc(staticCred.Host),
			"envvar:PORT": b64enc(staticCred.Port),
			"envvar:USER": b64enc(userRole),
			"envvar:PASS": b64enc(userRolePwd),
			"envvar:DB":   b64enc("postgres"),
		},
		ConnectionTags: map[string]string{
			"hoopdev/managed-by": featureName,
		},
	})
	return err
}

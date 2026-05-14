package resources

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
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

	creds, err := pgCredentialsFromResource(resource.Envs)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusUnprocessableEntity, err, "invalid resource credentials: %v", err)
		return
	}
	resp, err := transportsystem.BareExec(
		map[string]string{},
		pbsystem.UserInfo{},
		&pbsystem.BareExecRequest{
			SID:     uuid.NewString(),
			AgentID: resource.AgentID.String,
			Script:  resourceHealthCheckTest(resource.SubType.String),
			Command: resourceManagerCommand(resource.SubType.String),
			EnvVars: map[string]string{
				"HOST":       creds.Host,
				"PORT":       creds.Port,
				"USER":       creds.MasterUser,
				"PGPASSWORD": creds.MasterPwd,
			},
		},
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
	result, httpStatus := processSinglePlan(ctx, resourceName, req)
	if httpStatus != http.StatusOK {
		c.JSON(httpStatus, gin.H{"message": result.Message})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ResourcePlanBatch
//
//	@Summary		Creates a batch resource plan
//	@Description	Validates provisioning plans for a batch of resources by executing a SELECT 1 test query against each target database. Each item in the batch is session-audited and receives its own plan ID. All items are processed concurrently and the response is returned once all have completed.
//	@Tags			Resources
//	@Accept			json
//	@Produces		json
//	@Param			request		body		openapi.ResourcePlanRequest		true	"The request body"
//	@Success		200			{object}	openapi.ResourcePlanResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/resources/plan [post]
func ResourcePlanBatch(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.ResourcePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	for i, item := range req.Items {
		if item.ResourceName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("item[%d]: resource_name is required", i)})
			return
		}
		var unsupportedPrivileges []string
		for _, priv := range item.Privileges {
			if _, ok := supportedPrivileges[priv]; !ok {
				unsupportedPrivileges = append(unsupportedPrivileges, priv)
			}
		}
		if len(unsupportedPrivileges) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("item[%d]: found unsupported privileges=%v", i, unsupportedPrivileges)})
			return
		}
	}

	type indexedResult struct {
		index int
		res   openapi.ResourcePlanResult
	}

	resultCh := make(chan indexedResult, len(req.Items))
	var wg sync.WaitGroup

	for i, item := range req.Items {
		wg.Add(1)
		go func(idx int, planItem openapi.ResourcePlanItem) {
			defer wg.Done()
			res, _ := processSinglePlan(ctx, planItem.ResourceName, &planItem)
			resultCh <- indexedResult{index: idx, res: res}
		}(i, item)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	results := make([]openapi.ResourcePlanResult, len(req.Items))
	for r := range resultCh {
		results[r.index] = r.res
	}

	c.JSON(http.StatusOK, openapi.ResourcePlanResponse{Results: results})
}

// processSinglePlan executes a plan request for a single resource and returns the result.
// The second return value is an HTTP status code: http.StatusOK on success, or an error status.
func processSinglePlan(ctx *storagev2.Context, resourceName string, req *openapi.ResourcePlanItem) (openapi.ResourcePlanResult, int) {
	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, resourceName, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return openapi.ResourcePlanResult{
			ResourceName: resourceName,
			Role:         req.Role,
			Status:       "failed",
			Message:      fmt.Sprintf("failed retrieving resource: %v", err),
		}, http.StatusInternalServerError
	}
	if resource == nil {
		return openapi.ResourcePlanResult{
			ResourceName: resourceName,
			Role:         req.Role,
			Status:       "failed",
			Message:      "resource not found: " + resourceName,
		}, http.StatusNotFound
	}
	creds, err := pgCredentialsFromResource(resource.Envs)
	if err != nil {
		return openapi.ResourcePlanResult{
			ResourceName: resourceName,
			Role:         req.Role,
			Status:       "failed",
			Message:      err.Error(),
		}, http.StatusUnprocessableEntity
	}

	roleName, err := generateSecurePostgresRoleName(resource.Name, req.Role)
	if err != nil {
		return openapi.ResourcePlanResult{
			ResourceName: resourceName,
			Role:         req.Role,
			Status:       "failed",
			Message:      fmt.Sprintf("failed generating secure postgres role name: %v", err),
		}, http.StatusInternalServerError
	}

	requestPayload, _ := json.MarshalIndent(req, "", "  ")
	sess, err := openSession(roleName, resource.Type, resource.SubType.String, string(requestPayload), ctx)
	if err != nil {
		return openapi.ResourcePlanResult{
			ResourceName: resourceName,
			Role:         roleName,
			Status:       "failed",
			Message:      fmt.Sprintf("failed creating session: %v", err),
		}, http.StatusInternalServerError
	}

	resp := transportsystem.RunPgManagerPlan(resource.AgentID.String, &pbsystem.PgManagerPlanRequest{
		SID:            sess.SID(),
		RoleName:       roleName,
		SourceRole:     req.SourceRole,
		Type:           req.Type,
		Scopes:         req.Scopes,
		Privileges:     req.Privileges,
		PgCredentials:  creds,
		RotatePassword: req.RotatePassword,
	})

	sessionOutput := resp.Message // message is set only on error
	if len(resp.StateMigration) > 0 {
		sessionOutput = string(resp.StateMigration)
	}

	exitCode := 0
	if resp.Status != "success" {
		exitCode = 1
	}

	if err := sess.close(sessionOutput, exitCode); err != nil {
		log.With("sid", sess.SID()).Errorf("failed closing session for pg manager plan: %v", err)
	}

	return openapi.ResourcePlanResult{
		SID:          sess.SID(),
		ResourceName: resourceName,
		Role:         roleName,
		Status:       resp.Status,
		Message:      resp.Message,
	}, http.StatusOK
}

func ResourceApply(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ResourceApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	resourceName := c.Param("name")
	result, httpStatus := processSingleApply(ctx, resourceName, req.SID)
	if httpStatus != http.StatusOK {
		c.JSON(httpStatus, gin.H{"message": result.Message})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ResourceApplyBatch
//
//	@Summary		Applies a batch of resource plans
//	@Description	Applies a batch of previously created resource plans. Each item references a plan session by SID and the target resource name. All items are processed concurrently and the response is returned once all have completed.
//	@Tags			Resources
//	@Accept			json
//	@Produces		json
//	@Param			request		body		openapi.ResourceApplyBatchRequest	true	"The request body"
//	@Success		200			{object}	openapi.ResourceApplyBatchResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/resources/apply [post]
func ResourceApplyBatch(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.ResourceApplyBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	for i, item := range req.Items {
		if item.ResourceName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("item[%d]: resource_name is required", i)})
			return
		}
	}

	type indexedResult struct {
		index int
		res   openapi.ResourceApplyResult
	}

	resultCh := make(chan indexedResult, len(req.Items))
	var wg sync.WaitGroup

	for i, item := range req.Items {
		wg.Add(1)
		go func(idx int, applyItem openapi.ResourceApplyRequest) {
			defer wg.Done()
			res, _ := processSingleApply(ctx, applyItem.ResourceName, applyItem.SID)
			resultCh <- indexedResult{index: idx, res: res}
		}(i, item)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	results := make([]openapi.ResourceApplyResult, len(req.Items))
	for r := range resultCh {
		results[r.index] = r.res
	}

	c.JSON(http.StatusOK, openapi.ResourceApplyBatchResponse{Results: results})
}

// processSingleApply executes an apply request for a single resource and returns the result.
// The second return value is an HTTP status code: http.StatusOK on success, or an error status.
func processSingleApply(ctx *storagev2.Context, resourceName, planSID string) (openapi.ResourceApplyResult, int) {
	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, resourceName, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return openapi.ResourceApplyResult{
			ResourceName: resourceName,
			Status:       "failed",
			Message:      fmt.Sprintf("failed retrieving resource: %v", err),
		}, http.StatusInternalServerError
	}
	if resource == nil {
		return openapi.ResourceApplyResult{
			ResourceName: resourceName,
			Status:       "failed",
			Message:      "resource not found: " + resourceName,
		}, http.StatusNotFound
	}

	planSession, err := models.GetSessionByID(ctx.OrgID, planSID)
	if err != nil {
		return openapi.ResourceApplyResult{
			ResourceName: resourceName,
			Status:       "failed",
			Message:      fmt.Sprintf("failed retrieving session %v: %v", planSID, err),
		}, http.StatusNotFound
	}
	if planSession.ConnectionSubtype != "postgres" {
		return openapi.ResourceApplyResult{
			ResourceName: resourceName,
			Status:       "failed",
			Message:      fmt.Sprintf("resource type not implemented: %q", planSession.ConnectionSubtype),
		}, http.StatusUnprocessableEntity
	}
	// TODO: validate session tags
	// TODO: validate plan expiration date

	creds, err := pgCredentialsFromResource(resource.Envs)
	if err != nil {
		return openapi.ResourceApplyResult{
			ResourceName: resourceName,
			Status:       "failed",
			Message:      err.Error(),
		}, http.StatusUnprocessableEntity
	}

	blob, err := planSession.GetBlobStream()
	if err != nil {
		return openapi.ResourceApplyResult{
			ResourceName: resourceName,
			Status:       "failed",
			Message:      fmt.Sprintf("failed obtaining blob stream: %v", err),
		}, http.StatusInternalServerError
	}
	stateMigration, err := parseEventStreamPlan(blob)
	if err != nil {
		return openapi.ResourceApplyResult{
			ResourceName: resourceName,
			Status:       "failed",
			Message:      fmt.Sprintf("failed parsing session plan blob stream: %v", err),
		}, http.StatusInternalServerError
	}
	// TODO: validate tags!

	sess, err := openSession(planSession.Connection, resource.Type, resource.SubType.String, string(stateMigration), ctx)
	if err != nil {
		return openapi.ResourceApplyResult{
			ResourceName: resourceName,
			Status:       "failed",
			Message:      fmt.Sprintf("failed creating session: %v", err),
		}, http.StatusInternalServerError
	}

	resp := transportsystem.RunPgManagerApply(resource.AgentID.String, &pbsystem.PgManagerApplyRequest{
		SID:            sess.SID(),
		StateMigration: stateMigration,
		PgCredentials:  creds,
	})

	sessionOutput := resp.Message // message is set only on error
	if len(resp.StateMigration) > 0 {
		sessionOutput = string(resp.StateMigration)
	}

	status := resp.Status
	if resp.Status == "success" {
		switch planSession.ConnectionSubtype {
		case "postgres":
			err := syncPostgresResourceRole(ctx, resourceName, resp.RoleName, resource.AgentID.String,
				resp.RoleName, resp.RolePassword, creds)
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
		log.With("sid", sess.SID()).Errorf("failed closing session for pg manager apply: %v", err)
	}

	return openapi.ResourceApplyResult{
		SID:          sess.SID(),
		ResourceName: resourceName,
		Status:       status,
		Message:      resp.Message,
	}, http.StatusOK
}

// pgCredentialsFromResource builds PgCredentials from the resource env vars.
// Keys are expected in the form "envvar:HOST", "envvar:PORT", "envvar:USER",
// "envvar:PASS", and "envvar:SSLMODE" with base64-encoded values.
// Returns an error if any of the required fields (HOST, PORT, USER, PASS) are missing.
func pgCredentialsFromResource(envs map[string]string) (pbsystem.PgCredentials, error) {
	dec := func(v string) string {
		b, _ := base64.StdEncoding.DecodeString(v)
		return string(b)
	}
	creds := pbsystem.PgCredentials{
		Host:       dec(envs["envvar:HOST"]),
		Port:       dec(envs["envvar:PORT"]),
		MasterUser: dec(envs["envvar:USER"]),
		MasterPwd:  dec(envs["envvar:PASS"]),
	}
	if sslmode := dec(envs["envvar:SSLMODE"]); sslmode != "" {
		creds.Options = map[string]string{"sslmode": sslmode}
	}
	var missing []string
	if creds.Host == "" {
		missing = append(missing, "HOST")
	}
	if creds.Port == "" {
		missing = append(missing, "PORT")
	}
	if creds.MasterUser == "" {
		missing = append(missing, "USER")
	}
	if creds.MasterPwd == "" {
		missing = append(missing, "PASS")
	}
	if len(missing) > 0 {
		return pbsystem.PgCredentials{}, fmt.Errorf("resource is missing required credentials: %v", missing)
	}
	return creds, nil
}

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
		ID:         s.SID(),
		OrgID:      s.session.OrgID,
		Metrics:    map[string]any{},
		BlobStream: toEventStream(output),
		Status:     "done",
		ExitCode:   &exitCode,
		EndSession: &endTime,
	})
}

func syncPostgresResourceRole(ctx *storagev2.Context, resourceName, resourceRole, agentID, userRole, userRolePwd string, creds pbsystem.PgCredentials) error {
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
			"envvar:HOST": b64enc(creds.Host),
			"envvar:PORT": b64enc(creds.Port),
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

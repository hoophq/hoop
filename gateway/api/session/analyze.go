package sessionapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/aianalyzer"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// heldVerdictTTL is how long POST /sessions reuses a verdict of POST
// /sessions/analyze: the time a user takes to fill in the run forms.
const heldVerdictTTL = 15 * time.Minute

type heldVerdict struct {
	// analysis keeps the risk level. POST /sessions applies the current rule
	// to it, so a rule changed since the analysis applies to the run.
	analysis  *models.SessionAIAnalysis
	expiresAt time.Time
}

// verdictHold keeps, in the memory of one gateway and per org, the verdicts
// that POST /sessions/analyze let through. A miss (other replica, expired,
// restart) analyzes again, so no result depends on a hit.
type verdictHold struct {
	mu      sync.Mutex
	entries map[string]map[string]heldVerdict // org id -> key -> verdict
	now     func() time.Time
}

var heldVerdicts = &verdictHold{entries: map[string]map[string]heldVerdict{}, now: time.Now}

// put keeps v under key and drops the expired entries, so abandoned forms do
// not grow the map.
func (h *verdictHold) put(orgID, key string, v heldVerdict) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	for org, verdicts := range h.entries {
		for k, e := range verdicts {
			if !now.Before(e.expiresAt) {
				delete(verdicts, k)
			}
		}
		if len(verdicts) == 0 {
			delete(h.entries, org)
		}
	}
	if h.entries[orgID] == nil {
		h.entries[orgID] = map[string]heldVerdict{}
	}
	h.entries[orgID][key] = v
}

// get returns the unexpired verdict under key and keeps it: a run that fails
// before it starts (422 on a form field) can use it again.
func (h *verdictHold) get(orgID, key string) (heldVerdict, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.entries[orgID][key]
	if !ok || !h.now().Before(v.expiresAt) {
		return heldVerdict{}, false
	}
	return v, true
}

func (h *verdictHold) drop(orgID, key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.entries[orgID], key)
}

func (h *verdictHold) dropOrg(orgID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.entries, orgID)
}

// heldVerdictKey binds a verdict to the user, the connection and the exact
// script it judged.
func heldVerdictKey(userID, connectionID, script string) string {
	h := sha256.New()
	for _, part := range []string{userID, connectionID, script} {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// heldAnalysis returns the verdict POST /sessions/analyze held for this run,
// with the action of the connection's current rule. It does not spend the
// verdict. While the flag is off it drops the org's verdicts and holds none.
func heldAnalysis(ctx *storagev2.Context, conn *models.Connection, script string) (*models.SessionAIAnalysis, *models.AccessRequestRule, bool, error) {
	if !featureflag.IsEnabled(ctx.OrgID, featureflag.FlagAnalyzeBeforeRunForms) {
		heldVerdicts.dropOrg(ctx.OrgID)
		return nil, nil, false, nil
	}
	key := heldVerdictKey(ctx.UserID, conn.ID, script)
	v, ok := heldVerdicts.get(ctx.OrgID, key)
	if !ok {
		return nil, nil, false, nil
	}
	orgID := uuid.MustParse(ctx.GetOrgID())
	rule, err := models.GetAISessionAnalyzerRuleByConnection(models.DB, orgID, conn.Name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, false, fmt.Errorf("failed obtaining ai session analyzer rule for connection %v, reason: %w", conn.Name, err)
	}
	if rule == nil {
		// The rule is gone: analyze as if nothing was held.
		heldVerdicts.drop(ctx.OrgID, key)
		return nil, nil, false, nil
	}
	analysis := *v.analysis
	a, accessRule, err := applyRuleTier(orgID, rule, &analysis)
	return a, accessRule, true, err
}

// spendHeldVerdict drops the verdict of a run that is decided: blocked, sent
// to approval, or started. The next run of the same script analyzes again.
func spendHeldVerdict(ctx *storagev2.Context, conn *models.Connection, script string) {
	heldVerdicts.drop(ctx.OrgID, heldVerdictKey(ctx.UserID, conn.ID, script))
}

// newExecSession builds the session row of an exec request, and returns the
// user agent the exec client sends to the agent.
func newExecSession(c *gin.Context, ctx *storagev2.Context, sid string, req SessionPostBody, conn *models.Connection) (models.Session, string) {
	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	// Attribute the session to the calling surface via the User-Client/User-Agent
	// header: the webapp (CLJS) sends "webapp.core", the CLI sends "hoopcli", and
	// everything else is treated as a raw API consumer. Once webapp_v2 (React)
	// issues its own session calls it must send the same header to keep being
	// attributed as `webapp`.
	sessionOrigin := proto.SessionOriginFromUserAgent(userAgent)
	if userAgent == "webapp.core" {
		userAgent = "webapp.editor.exec"
	}
	return models.Session{
		ID:                   sid,
		OrgID:                ctx.OrgID,
		Labels:               req.Labels,
		Metadata:             req.Metadata,
		IntegrationsMetadata: nil,
		Metrics:              nil,
		BlobInput:            models.BlobInputType(req.Script),
		UserEmail:            ctx.UserEmail,
		UserID:               ctx.UserID,
		UserName:             ctx.UserName,
		ConnectionType:       conn.Type,
		ConnectionSubtype:    conn.SubType.String,
		Connection:           conn.Name,
		ConnectionTags:       conn.ConnectionTags,
		Verb:                 proto.ClientVerbExec,
		Status:               string(openapi.SessionStatusOpen),
		IdentityType:         "user",
		SessionBatchID:       req.SessionBatchID,
		CorrelationID:        req.CorrelationID,
		Origin:               sessionOrigin,
		CreatedAt:            time.Now().UTC(),
		EndSession:           nil,
	}, userAgent
}

func newAIAnalyzeInput(c *gin.Context, ctx *storagev2.Context, conn *models.Connection, script string) AIAnalyzeInput {
	return AIAnalyzeInput{
		OrgID:          uuid.MustParse(ctx.GetOrgID()),
		ConnectionName: conn.Name,
		Script:         script,
		UserID:         ctx.UserID,
		UserGroups:     ctx.UserGroups,
		Exec:           aianalyzer.AnalyzerExecIdentity{BearerToken: apiroutes.GetAccessTokenFromRequest(c), UserAgent: "aianalyzer"},
	}
}

// Analyze
//
//	@Summary		Analyze an exec before the run forms
//	@Description	Run the AI Session Analyzer on the script of a `POST /sessions` body, before the client asks for the run forms (Jira template, required metadata). A block is final: the gateway records the blocked session as `POST /sessions` does. Any other verdict records nothing, and the next `POST /sessions` of the same user, connection and script reuses it for 15 minutes on the same gateway. Answers 412 while `experimental.analyze_before_run_forms` is off.
//	@Tags			Sessions
//	@Accept			json
//	@Produce		json
//	@Param			request				body		openapi.ExecRequest		true	"The request body resource"
//	@Success		200					{object}	openapi.ExecResponse	"The analyzer blocked the script"
//	@Success		204					"The analyzer lets the script run, or the connection has no analyzer rule"
//	@Failure		400,412,422,500		{object}	openapi.HTTPError
//	@Router			/sessions/analyze [post]
func Analyze(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if !featureflag.IsEnabled(ctx.OrgID, featureflag.FlagAnalyzeBeforeRunForms) {
		c.JSON(http.StatusPreconditionFailed, gin.H{"message": "turn on " +
			featureflag.FlagAnalyzeBeforeRunForms + " in Settings -> Experimental"})
		return
	}

	var req SessionPostBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := CoerceMetadataFields(req.Metadata); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if err := ValidateCorrelationID(req.CorrelationID); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, req.Connection)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetch connection %v for analysis, err=%v", req.Connection, err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("connection %v not found", req.Connection)})
		return
	}

	analysis, accessRule, err := AIAnalyze(c, newAIAnalyzeInput(c, ctx, conn, req.Script))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed analyzing session")
		return
	}
	if analysis == nil {
		c.Status(http.StatusNoContent)
		return
	}

	if analysis.Action == string(models.BlockExecution) {
		sid := uuid.NewString()
		apiroutes.SetSidSpanAttr(c, sid)
		session, _ := newExecSession(c, ctx, sid, req, conn)
		decision, resp, err := ApplyAIAnalysisDecision(ctx, &session, conn, analysis, accessRule, req.EnvVars, req.ClientArgs)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed enforcing ai analysis")
			return
		}
		if decision != AIDecisionBlocked {
			err := fmt.Errorf("block_execution verdict resolved to decision %v", decision)
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed enforcing ai analysis")
			return
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	heldVerdicts.put(ctx.OrgID, heldVerdictKey(ctx.UserID, conn.ID, req.Script), heldVerdict{
		analysis:  analysis,
		expiresAt: heldVerdicts.now().Add(heldVerdictTTL),
	})
	c.Status(http.StatusNoContent)
}

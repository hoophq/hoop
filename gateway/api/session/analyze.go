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
	"github.com/hoophq/hoop/common/log"
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

// heldVerdictSweep is how often a put drops the expired verdicts of every org.
const heldVerdictSweep = time.Minute

type heldVerdict struct {
	analysis *models.SessionAIAnalysis
	// ruleID and ruleUpdatedAt name the rule version that judged the script.
	// A rule changed since (risk actions, custom prompt, agentic) analyzes again.
	ruleID        uuid.UUID
	ruleUpdatedAt time.Time
	expiresAt     time.Time
}

// sameRule reports whether v and o were judged by the same rule version.
func (v heldVerdict) sameRule(o heldVerdict) bool {
	return v.ruleID == o.ruleID && v.ruleUpdatedAt.Equal(o.ruleUpdatedAt)
}

// strictness ranks an action: block, then approval, then allow.
func strictness(action string) int {
	switch models.RiskEvaluationAction(action) {
	case models.BlockExecution:
		return 2
	case models.RequireAccessRequest:
		return 1
	}
	return 0
}

// verdictHold keeps, in the memory of one gateway and per org, the verdicts
// that POST /sessions/analyze let through. A miss (other replica, expired,
// restart) analyzes again, so no result depends on a hit. now is monotonic
// (time.Now): the TTL must not follow a wall clock step.
type verdictHold struct {
	mu        sync.Mutex
	entries   map[string]map[string]heldVerdict // org id -> key -> verdict
	now       func() time.Time
	lastSweep time.Time
}

var heldVerdicts = &verdictHold{entries: map[string]map[string]heldVerdict{}, now: time.Now}

// put keeps v under key. A held verdict for the same rule version that is
// stricter stays: analyzing again until the model answers allow does not
// replace an approval verdict.
func (h *verdictHold) put(orgID, key string, v heldVerdict) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if now.Sub(h.lastSweep) >= heldVerdictSweep {
		h.lastSweep = now
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
	}
	if h.entries[orgID] == nil {
		h.entries[orgID] = map[string]heldVerdict{}
	}
	held, ok := h.entries[orgID][key]
	if ok && now.Before(held.expiresAt) && held.sameRule(v) &&
		strictness(held.analysis.Action) > strictness(v.analysis.Action) {
		return
	}
	h.entries[orgID][key] = v
}

// take removes and returns the unexpired verdict under key, so one run uses
// it. A run that fails before it is decided gives it back with restore.
func (h *verdictHold) take(orgID, key string) (heldVerdict, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	verdicts := h.entries[orgID]
	v, ok := verdicts[key]
	if !ok {
		return heldVerdict{}, false
	}
	delete(verdicts, key)
	if len(verdicts) == 0 {
		delete(h.entries, orgID)
	}
	if !h.now().Before(v.expiresAt) {
		return heldVerdict{}, false
	}
	return v, true
}

// restore gives back a taken verdict, unless a newer one took its key.
func (h *verdictHold) restore(orgID, key string, v heldVerdict) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.now().Before(v.expiresAt) {
		return
	}
	if h.entries[orgID] == nil {
		h.entries[orgID] = map[string]heldVerdict{}
	}
	if _, ok := h.entries[orgID][key]; !ok {
		h.entries[orgID][key] = v
	}
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

// heldRun is a held verdict that one POST /sessions took.
type heldRun struct {
	orgID, key string
	verdict    heldVerdict
	analysis   *models.SessionAIAnalysis
	accessRule *models.AccessRequestRule
}

// release gives the verdict back, for a run that failed before it was decided
// (a 422 on a form field): the retry reuses it.
func (r *heldRun) release() { heldVerdicts.restore(r.orgID, r.key, r.verdict) }

// takeHeldVerdict takes the verdict POST /sessions/analyze held for this run,
// while the connection's rule is the version that judged it. It returns nil
// when there is none. While the flag is off it drops the org's verdicts.
func takeHeldVerdict(ctx *storagev2.Context, conn *models.Connection, script string) (*heldRun, error) {
	if !featureflag.IsEnabled(ctx.OrgID, featureflag.FlagAnalyzeBeforeRunForms) {
		heldVerdicts.dropOrg(ctx.OrgID)
		return nil, nil
	}
	key := heldVerdictKey(ctx.UserID, conn.ID, script)
	v, ok := heldVerdicts.take(ctx.OrgID, key)
	if !ok {
		return nil, nil
	}
	orgID := uuid.MustParse(ctx.GetOrgID())
	rule, err := models.GetAISessionAnalyzerRuleByConnection(models.DB, orgID, conn.Name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		heldVerdicts.restore(ctx.OrgID, key, v)
		return nil, fmt.Errorf("failed obtaining ai session analyzer rule for connection %v, reason: %w", conn.Name, err)
	}
	if rule == nil || !v.sameRule(heldVerdict{ruleID: rule.ID, ruleUpdatedAt: rule.UpdatedAt}) {
		// The rule is gone or changed: analyze as if nothing was held.
		return nil, nil
	}
	analysis := *v.analysis
	a, accessRule, err := applyRuleTier(orgID, rule, &analysis)
	if err != nil {
		heldVerdicts.restore(ctx.OrgID, key, v)
		return nil, err
	}
	return &heldRun{orgID: ctx.OrgID, key: key, verdict: v, analysis: a, accessRule: accessRule}, nil
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

	analysis, accessRule, rule, err := aiAnalyze(c, newAIAnalyzeInput(c, ctx, conn, req.Script))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed analyzing session")
		return
	}
	if analysis == nil {
		c.Status(http.StatusNoContent)
		return
	}
	// Each verdict is logged: repeated analyze calls on a script leave a trail.
	log.With("user", ctx.UserEmail, "connection", conn.Name, "risk", analysis.RiskLevel, "action", analysis.Action).
		Infof("ai analysis before the run forms")

	if analysis.Action == string(models.BlockExecution) {
		sid := uuid.NewString()
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
		analysis:      analysis,
		ruleID:        rule.ID,
		ruleUpdatedAt: rule.UpdatedAt,
		expiresAt:     heldVerdicts.now().Add(heldVerdictTTL),
	})
	c.Status(http.StatusNoContent)
}

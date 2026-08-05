package apireports

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// complianceSnapshot is the point-in-time input for all check evaluators.
type complianceSnapshot struct {
	AuthMethod        string // idptypes.ProviderType as string: "local","oidc","idp","saml"
	Connections       []models.Connection
	GroupNames        []string // distinct group names
	Users             []models.User
	Agents            []models.Agent
	GuardrailCount    int
	PendingReviews    int
	ServiceAccounts   []models.ServiceAccount
	WebhookConfigured bool
	SessionMetrics    *models.SessionMetricsAggregatedResult // last 30 days
	GatewayVersion    string
}

func collectComplianceSnapshot(ctx *storagev2.Context) (*complianceSnapshot, error) {
	_, providerType, err := idp.LoadServerAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed loading server auth config: %v", err)
	}

	connections, err := models.ListConnections(ctx, models.ConnectionFilterOption{})
	if err != nil {
		return nil, fmt.Errorf("failed listing connections: %v", err)
	}

	userGroups, err := models.GetUserGroupsByOrgID(ctx.OrgID)
	if err != nil {
		return nil, fmt.Errorf("failed listing user groups: %v", err)
	}
	groupSeen := map[string]bool{}
	var groupNames []string
	for _, g := range userGroups {
		if !groupSeen[g.Name] {
			groupSeen[g.Name] = true
			groupNames = append(groupNames, g.Name)
		}
	}

	users, err := models.ListUsers(ctx.OrgID)
	if err != nil {
		return nil, fmt.Errorf("failed listing users: %v", err)
	}

	agents, err := models.ListAgents(ctx.OrgID, "")
	if err != nil {
		return nil, fmt.Errorf("failed listing agents: %v", err)
	}

	guardrails, err := models.ListGuardRailRules(ctx.OrgID, models.GuardRailListOption{IncludeAllRulepackOwned: true})
	if err != nil {
		return nil, fmt.Errorf("failed listing guardrail rules: %v", err)
	}

	reviews, err := models.ListReviews(ctx.OrgID)
	if err != nil {
		return nil, fmt.Errorf("failed listing reviews: %v", err)
	}
	pendingReviews := 0
	if reviews != nil {
		for _, rev := range *reviews {
			if rev.Status == models.ReviewStatusPending {
				pendingReviews++
			}
		}
	}

	serviceAccounts, err := models.ListServiceAccounts(ctx.OrgID)
	if err != nil {
		return nil, fmt.Errorf("failed listing service accounts: %v", err)
	}

	thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30)
	sessionMetrics, err := models.GetSessionMetricsAggregated(ctx.OrgID, models.SessionMetricsFilter{
		StartDate:           &thirtyDaysAgo,
		IncludeOpenSessions: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed aggregating session metrics: %v", err)
	}

	conf := appconfig.Get()
	return &complianceSnapshot{
		AuthMethod:        string(providerType),
		Connections:       connections,
		GroupNames:        groupNames,
		Users:             users,
		Agents:            agents,
		GuardrailCount:    len(guardrails),
		PendingReviews:    pendingReviews,
		ServiceAccounts:   serviceAccounts,
		WebhookConfigured: conf.WebhookAppKey() != "" || conf.WebhookAppURL() != nil,
		SessionMetrics:    sessionMetrics,
		GatewayVersion:    version.Get().Version,
	}, nil
}

// statusWeight returns the scoring weight of a status and whether it counts
// toward the applicable denominator.
func statusWeight(s openapi.ComplianceStatusType) (weight float64, applicable bool) {
	switch s {
	case openapi.ComplianceStatusCompliant:
		return 1.0, true
	case openapi.ComplianceStatusWarning, openapi.ComplianceStatusIdpDependent:
		return 0.5, true
	case openapi.ComplianceStatusNonCompliant:
		return 0, true
	default: // not_applicable, unable_to_verify
		return 0, false
	}
}

func scoreLevel(score, lowMax, moderateMax int) string {
	switch {
	case score <= lowMax:
		return "low"
	case score <= moderateMax:
		return "moderate"
	default:
		return "strong"
	}
}

func buildComplianceReport(snap *complianceSnapshot) openapi.ComplianceReport {
	results := evalComplianceChecks(snap)

	var (
		frameworks           []openapi.ComplianceFramework
		overallWeight        float64
		overallCompliant     int
		overallApplicable    int
		firstActionByCheckID = map[string]openapi.ComplianceAction{}
	)

	for _, fwDef := range complianceFrameworks {
		var (
			fw         = openapi.ComplianceFramework{ID: fwDef.ID, Name: fwDef.Name}
			weightSum  float64
			applicable int
		)
		for _, groupDef := range fwDef.Groups {
			group := openapi.ComplianceControlGroup{ID: groupDef.ID, Title: groupDef.Title}
			for _, ctrlDef := range groupDef.Controls {
				res := results[ctrlDef.CheckID]
				category := complianceCheckByID[ctrlDef.CheckID].Category
				group.Controls = append(group.Controls, openapi.ComplianceControl{
					ID:          ctrlDef.ID,
					Title:       ctrlDef.Title,
					Description: ctrlDef.Description,
					CheckID:     ctrlDef.CheckID,
					Category:    category,
					Status:      res.Status,
					Message:     res.Message,
					Evidence:    res.Evidence,
					Action:      ctrlDef.Action,
				})
				if _, ok := firstActionByCheckID[ctrlDef.CheckID]; !ok {
					firstActionByCheckID[ctrlDef.CheckID] = ctrlDef.Action
				}

				switch res.Status {
				case openapi.ComplianceStatusCompliant:
					fw.Breakdown.Compliant++
				case openapi.ComplianceStatusWarning:
					fw.Breakdown.Warning++
				case openapi.ComplianceStatusNonCompliant:
					fw.Breakdown.NonCompliant++
				case openapi.ComplianceStatusNotApplicable:
					fw.Breakdown.NotApplicable++
				case openapi.ComplianceStatusUnableToVerify:
					fw.Breakdown.UnableToVerify++
				case openapi.ComplianceStatusIdpDependent:
					fw.Breakdown.IdpDependent++
				}
				w, isApplicable := statusWeight(res.Status)
				if isApplicable {
					weightSum += w
					applicable++
				}
			}
			fw.Groups = append(fw.Groups, group)
		}
		fw.Compliant = fw.Breakdown.Compliant
		fw.TotalApplicable = applicable
		if applicable > 0 {
			fw.ScorePercent = int(math.Round(100 * weightSum / float64(applicable)))
		}
		fw.Level = scoreLevel(fw.ScorePercent, 39, 69)
		frameworks = append(frameworks, fw)

		overallWeight += weightSum
		overallCompliant += fw.Breakdown.Compliant
		overallApplicable += applicable
	}

	overall := openapi.ComplianceOverall{
		Compliant:       overallCompliant,
		TotalApplicable: overallApplicable,
	}
	if overallApplicable > 0 {
		overall.Score = int(math.Round(1000 * overallWeight / float64(overallApplicable)))
	}
	overall.Level = scoreLevel(overall.Score, 499, 749)

	var categories []openapi.ComplianceCategorySummary
	for _, cat := range complianceCategories {
		summary := openapi.ComplianceCategorySummary{ID: cat.ID, Title: cat.Title}
		for _, check := range complianceChecks {
			if check.Category != cat.ID {
				continue
			}
			status := results[check.ID].Status
			if status == openapi.ComplianceStatusNotApplicable || status == openapi.ComplianceStatusUnableToVerify {
				continue
			}
			summary.Total++
			if status == openapi.ComplianceStatusCompliant {
				summary.Compliant++
			}
		}
		categories = append(categories, summary)
	}

	var actionRequired []openapi.ComplianceCheckResult
	for _, check := range complianceChecks {
		res := results[check.ID]
		if res.Status != openapi.ComplianceStatusWarning && res.Status != openapi.ComplianceStatusNonCompliant {
			continue
		}
		action, ok := firstActionByCheckID[check.ID]
		if !ok || (action.Type != "app" && action.Type != "docs") {
			continue
		}
		actionRequired = append(actionRequired, openapi.ComplianceCheckResult{
			ID:       check.ID,
			Title:    check.Title,
			Category: check.Category,
			Status:   res.Status,
			Message:  res.Message,
			Evidence: res.Evidence,
			Action:   action,
		})
	}
	// non_compliant first, then warning; stable to preserve catalog order.
	sort.SliceStable(actionRequired, func(i, j int) bool {
		return actionRequired[i].Status == openapi.ComplianceStatusNonCompliant &&
			actionRequired[j].Status != openapi.ComplianceStatusNonCompliant
	})

	return openapi.ComplianceReport{
		GeneratedAt:    time.Now().UTC(),
		Overall:        overall,
		Categories:     categories,
		ActionRequired: actionRequired,
		Frameworks:     frameworks,
	}
}

// ComplianceReport
//
//	@Summary		Get Compliance Report
//	@Description	Computes the compliance report with an overall score, category summaries, actionable items and per-framework control evaluations.
//	@Tags			Reports
//	@Produce		json
//	@Success		200	{object}	openapi.ComplianceReport
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/reports/compliance [get]
func ComplianceReport(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	snap, err := collectComplianceSnapshot(ctx)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed generating compliance report: %v", err)
		return
	}
	c.JSON(http.StatusOK, buildComplianceReport(snap))
}

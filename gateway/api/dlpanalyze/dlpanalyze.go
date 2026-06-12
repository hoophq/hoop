package apidlpanalyze

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"

	"libhoop/redactor"
	redactortypes "libhoop/redactor/types"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/guardrails"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// FeatureFlagName gates the /dlp/analyze endpoint.
const FeatureFlagName = "beta.dlp_analyze_api"

// maxConcurrentAnalyses bounds the number of in-flight Presidio requests per batch.
const maxConcurrentAnalyses = 5

// defaultEntityTypes is the fallback set of Presidio entity types used when the
// request doesn't specify entity types and no connection masking rules apply.
var defaultEntityTypes = []string{
	"PERSON",
	"EMAIL_ADDRESS",
	"PHONE_NUMBER",
	"LOCATION",
	"CREDIT_CARD",
	"IBAN_CODE",
	"IP_ADDRESS",
	"US_SSN",
	"US_BANK_NUMBER",
	"US_PASSPORT",
	"US_DRIVER_LICENSE",
	"CRYPTO",
}

// AnalyzeDLP
//
//	@Summary		Analyze Text for PII and Guardrails
//	@Description	Analyze caller-provided text entries for PII entities and guardrail rule violations using the DLP provider configured in this gateway (MSPresidio). Items are processed independently: a failure analyzing one item is reported in its result without failing the batch.
//	@Tags			DLP
//	@Accept			json
//	@Produce		json
//	@Param			request				body		openapi.DLPAnalyzeRequest	true	"The request body resource"
//	@Success		200					{object}	openapi.DLPAnalyzeResponse
//	@Failure		400,403,422,500,502	{object}	openapi.HTTPError
//	@Router			/dlp/analyze [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if !featureflag.IsEnabled(ctx.GetOrgID(), FeatureFlagName) {
		c.JSON(http.StatusForbidden, gin.H{"message": "this feature is not enabled for this organization"})
		return
	}

	var req openapi.DLPAnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	appcfg := appconfig.Get()
	if appcfg.DlpProvider() != "mspresidio" || appcfg.MSPresidioAnalyzerURL() == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": "the mspresidio DLP provider is not configured in this gateway"})
		return
	}

	opts, err := buildRedactorOpts(ctx.GetOrgID(), &req)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed resolving DLP analysis rules: %v", err)
		return
	}

	redactorClient, analyzerClient, err := redactor.NewClient(opts)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusBadGateway, err, "failed initializing the DLP client: %v", err)
		return
	}
	if analyzerClient == nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": "the DLP analyzer client could not be initialized"})
		return
	}

	results := analyzeItems(c.Request.Context(), redactorClient, analyzerClient, req.Items)
	c.JSON(http.StatusOK, &openapi.DLPAnalyzeResponse{Results: results})
}

// buildRedactorOpts assembles the libhoop redactor configuration the same way
// the gateway transport does when opening agent sessions (see
// gateway/transport/client.go), so analysis results are consistent with the
// proxy data flow.
func buildRedactorOpts(orgID string, req *openapi.DLPAnalyzeRequest) (map[string]string, error) {
	appcfg := appconfig.Get()
	opts := map[string]string{
		"dlp_provider":              appcfg.DlpProvider(),
		"dlp_mode":                  appcfg.DlpMode(),
		"mspresidio_analyzer_url":   appcfg.MSPresidioAnalyzerURL(),
		"mspresidio_anonymizer_url": appcfg.MSPresidioAnomymizerURL(),
	}

	metricsRules, err := analyzerMetricsRules(orgID, req)
	if err != nil {
		return nil, err
	}
	opts["analyzer_metrics_rules"] = string(metricsRules)

	guardRailRules, err := guardRailRulesJSON(orgID, req.Connection)
	if err != nil {
		return nil, err
	}
	if guardRailRules != nil {
		opts["guard_rail_rules"] = string(guardRailRules)
	}
	return opts, nil
}

// analyzerMetricsRules resolves the entity types used for the PII analysis,
// encoded in the []redactor.DataMaskingEntityData wire format expected by the
// analyzer_metrics_rules option. Precedence: request entity types, connection
// data masking rules, default entity set.
func analyzerMetricsRules(orgID string, req *openapi.DLPAnalyzeRequest) (json.RawMessage, error) {
	entityTypes := req.EntityTypes
	if len(entityTypes) == 0 && req.Connection != "" {
		connRules, err := services.GetDataMaskingRulesForConnection(orgID, req.Connection)
		if err != nil {
			return nil, err
		}
		if len(connRules) > 0 && string(connRules) != "[]" {
			return connRules, nil
		}
	}
	if len(entityTypes) == 0 {
		entityTypes = defaultEntityTypes
	}
	return json.Marshal([]redactor.DataMaskingEntityData{{
		SupportedEntityTypes: []redactor.SupportedEntityTypesEntry{{EntityTypes: entityTypes}},
	}})
}

// guardRailRuleWithMeta matches the preferred guard_rail_rules wire shape
// parsed by libhoop/redactor (GuardRailRuleWithMeta).
type guardRailRuleWithMeta struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	InputRules  []map[string]any `json:"input_rules,omitempty"`
	OutputRules []map[string]any `json:"output_rules,omitempty"`
}

// guardRailRulesJSON resolves the guardrail rules to validate against. When a
// connection is provided it uses the rules attached to that connection (legacy
// {input_rules,output_rules} shape, same as the transport layer). Otherwise it
// sends every guardrail rule of the org using the metadata-aware shape so
// violations carry the rule name.
func guardRailRulesJSON(orgID, connectionName string) (json.RawMessage, error) {
	if connectionName != "" {
		connRules, err := services.GetGuardRailRulesForConnection(orgID, connectionName)
		if err != nil {
			return nil, err
		}
		if connRules == nil {
			return nil, nil
		}
		var inputRules, outputRules []guardrails.DataRules
		if connRules.GuardRailInputRules != nil {
			if inputRules, err = guardrails.Decode(connRules.GuardRailInputRules); err != nil {
				return nil, err
			}
		}
		if connRules.GuardRailOutputRules != nil {
			if outputRules, err = guardrails.Decode(connRules.GuardRailOutputRules); err != nil {
				return nil, err
			}
		}
		if inputRules == nil && outputRules == nil {
			return nil, nil
		}
		return json.Marshal(struct {
			InputRules  []guardrails.DataRules `json:"input_rules"`
			OutputRules []guardrails.DataRules `json:"output_rules"`
		}{inputRules, outputRules})
	}

	rules, err := models.ListGuardRailRules(orgID)
	if err != nil {
		return nil, err
	}
	payload := make([]guardRailRuleWithMeta, 0, len(rules))
	for _, rule := range rules {
		entry := guardRailRuleWithMeta{ID: rule.ID, Name: rule.Name}
		if len(rule.Input) > 0 {
			entry.InputRules = []map[string]any{rule.Input}
		}
		if len(rule.Output) > 0 {
			entry.OutputRules = []map[string]any{rule.Output}
		}
		if len(entry.InputRules) == 0 && len(entry.OutputRules) == 0 {
			continue
		}
		payload = append(payload, entry)
	}
	if len(payload) == 0 {
		return nil, nil
	}
	return json.Marshal(payload)
}

// analyzeItems processes the batch with bounded concurrency, preserving the
// request order in the results.
func analyzeItems(ctx context.Context, redactorClient, analyzerClient redactortypes.Client, items []openapi.DLPAnalyzeItem) []openapi.DLPAnalyzeResult {
	results := make([]openapi.DLPAnalyzeResult, len(items))
	sem := make(chan struct{}, maxConcurrentAnalyses)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = analyzeItem(ctx, redactorClient, analyzerClient, item)
		}()
	}
	wg.Wait()
	return results
}

func analyzeItem(ctx context.Context, redactorClient, analyzerClient redactortypes.Client, item openapi.DLPAnalyzeItem) openapi.DLPAnalyzeResult {
	result := openapi.DLPAnalyzeResult{
		ID:         item.ID,
		Findings:   []openapi.DLPFinding{},
		Guardrails: []openapi.DLPGuardrailMatch{},
	}
	data := redactortypes.NewRequestDataText(item.Text)

	resp := analyzerClient.Analyze(ctx, data, redactortypes.CodecText)
	switch {
	case resp.Err != nil:
		result.Error = resp.Err.Error()
	default:
		byEntity := map[string]*openapi.DLPFinding{}
		for _, finding := range resp.Findings {
			entry, ok := byEntity[finding.EntityType]
			if !ok {
				entry = &openapi.DLPFinding{EntityType: finding.EntityType}
				byEntity[finding.EntityType] = entry
			}
			entry.Count++
			entry.Matches = append(entry.Matches, openapi.DLPFindingMatch{
				Start: finding.Start,
				End:   finding.End,
				Score: finding.Score,
			})
		}
		for _, entry := range byEntity {
			result.Findings = append(result.Findings, *entry)
		}
		sort.Slice(result.Findings, func(i, j int) bool {
			return result.Findings[i].EntityType < result.Findings[j].EntityType
		})
	}

	if item.GuardrailDirection == "" || redactorClient == nil {
		return result
	}
	guardResp := redactorClient.ValidateGuardRailRules(ctx, item.GuardrailDirection, data, redactortypes.CodecText)
	if guardResp.Err == nil {
		return result
	}
	var guardrailErr *redactortypes.ErrGuardrailsValidation
	if !errors.As(guardResp.Err, &guardrailErr) {
		if result.Error == "" {
			result.Error = guardResp.Err.Error()
		}
		return result
	}
	for _, info := range guardrailErr.Info() {
		result.Guardrails = append(result.Guardrails, openapi.DLPGuardrailMatch{
			RuleName:     info.RuleName,
			Direction:    info.Direction,
			RuleType:     info.Rule.Type,
			Words:        info.Rule.Words,
			PatternRegex: info.Rule.PatternRegex,
			MatchedWords: info.MatchedWords,
		})
	}
	return result
}

package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type ErrInvalidIssueFields struct {
	isRequiredErr bool
	resources     []string
}

func (e *ErrInvalidIssueFields) Error() string {
	if e.isRequiredErr {
		return fmt.Sprintf("unable to parse fields, missing required fields: %v", e.resources)
	}
	return fmt.Sprintf("unable to parse fields, invalid preset mapping types values: %v", e.resources)
}

func TransitionIssue(config *models.JiraIntegration, issueKey, name string) error {
	if config == nil {
		return nil
	}
	issueTransitions, err := listIssueTransitions(config, issueKey)
	if err != nil {
		return err
	}
	var availableNames []string
	var issueTransitionID string
	for _, item := range issueTransitions.Items {
		availableNames = append(availableNames, strings.ToLower(item.Name))
		if strings.EqualFold(name, item.Name) && item.IsAvailable {
			issueTransitionID = item.ID
			break
		}
	}
	if issueTransitionID == "" {
		return fmt.Errorf("unable to find a issue transition matching %v, issue=%v, transition-names=%v",
			name, issueKey, availableNames)
	}
	issueTransition := map[string]any{
		"transition": map[string]string{
			"id": issueTransitionID,
		},
	}
	jsonPayload, _ := json.Marshal(issueTransition)
	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", config.URL, issueKey)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed creating request, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(config.User, config.APIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed transitioning jira issue %s, reason=%v", issueKey, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unable to transition jira issue, status=%v, body=%v",
			resp.StatusCode, string(body))
	}
	return nil
}

func listIssueTransitions(config *models.JiraIntegration, issueKey string) (*IssueTransition, error) {
	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", config.URL, issueKey)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating issue transition request, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(config.User, config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed listing issue transitions for %s, reason=%v", issueKey, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unable to list transitions, api-url=%v, key=%v, status=%v, body=%v",
			apiURL, issueKey, resp.StatusCode, string(body))
	}
	var obj IssueTransition
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("failed decoding issue transitions, key=%s, reason=%v", issueKey, err)
	}
	return &obj, nil
}

func ParseIssueFields(tmpl *models.JiraIssueTemplate, input map[string]string, session types.Session) (CustomFields, error) {
	if input == nil {
		input = map[string]string{}
	}
	output := CustomFields{}
	mappingTypes, promptTypes, cmdbTypes, err := tmpl.DecodeMappingTypes()
	if err != nil {
		return nil, err
	}

	// handle prompt types
	invalidPresetFields, missingRequiredFields := []string{}, []string{}
	for jiraField, promptType := range promptTypes {
		val, ok := input[jiraField]
		if !ok {
			if promptType.Required {
				missingRequiredFields = append(missingRequiredFields, fmt.Sprintf("%q", jiraField))
			}
			continue
		}
		output[jiraField] = val
	}
	if len(missingRequiredFields) > 0 {
		return nil, &ErrInvalidIssueFields{isRequiredErr: true, resources: missingRequiredFields}
	}

	// handle mapping type fields
	presetFields := loadDefaultPresetFields(session)
	for jiraField, mappingType := range mappingTypes {
		switch mappingType.Type {
		case "preset":
			presetVal, ok := presetFields[mappingType.Value]
			if !ok {
				invalidPresetFields = append(invalidPresetFields, fmt.Sprintf("%q", mappingType.Value))
				continue
			}
			output[jiraField] = presetVal
		case "custom":
			output[jiraField] = mappingType.Value
		default:
			log.Warnf("mapping type (%v) not found", mappingType.Type)
		}
	}
	if len(invalidPresetFields) > 0 {
		return nil, &ErrInvalidIssueFields{resources: invalidPresetFields}
	}

	for jiraField, cmdbType := range cmdbTypes {
		val, ok := input[jiraField]
		if !ok {
			if cmdbType.Required {
				missingRequiredFields = append(missingRequiredFields, fmt.Sprintf("%q", jiraField))
			}
			continue
		}

		output[jiraField] = []map[string]string{{"id": val}}
	}
	if len(missingRequiredFields) > 0 {
		return nil, &ErrInvalidIssueFields{isRequiredErr: true, resources: missingRequiredFields}
	}

	return output, nil
}

func loadDefaultPresetFields(s types.Session) map[string]string {
	script := s.Script["data"]
	if len(s.Script) > 5000 {
		script = script[0:5000] + fmt.Sprintf(" ...[TRUNCATED %v]", len(script[5000:]))
	}
	return map[string]string{
		"session.id":         s.ID,
		"session.user_email": s.UserEmail,
		"session.user_id":    s.UserID,
		"session.user_name":  s.UserName,
		"session.type":       s.Type,
		// "session.subtype":    "",
		"session.connection":  s.Connection,
		"session.status":      s.Status,
		"session.script":      "{code:shell}" + script + "{code}",
		"session.start_date":  s.StartSession.Format(time.RFC3339),
		"session.webapp_link": appconfig.Get().ApiURL() + "/sessions/" + s.ID,
	}
}

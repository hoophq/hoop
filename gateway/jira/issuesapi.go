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
)

// The maximum size of a text are field is 32767 characters
// it should truncate the script above this size
const defaultScriptCharMaxSize int = 32700

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
	name = strings.ToLower(name)
	issueTransitions, err := listIssueTransitions(config, issueKey)
	if err != nil {
		return err
	}
	var availableNames []string
	var issueTransitionID string
	for _, item := range issueTransitions.Items {
		availableNames = append(availableNames,
			fmt.Sprintf("name=%v, isAvailable=%v, %s",
				strings.ToLower(item.Name), item.IsAvailable, item.To.String()))
		if strings.EqualFold(name, item.To.Name) && item.IsAvailable {
			issueTransitionID = item.ID
			break
		}
	}
	if issueTransitionID == "" {
		return fmt.Errorf("unable to find a issue transition matching %q, issue=%v, transition-names=%q",
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

func ParseIssueFields(tmpl *models.JiraIssueTemplate, input map[string]string, session models.Session) (CustomFields, error) {
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
		switch promptType.FieldType {
		case "select":
			output[jiraField] = map[string]string{"value": val}
		default:
			output[jiraField] = val
		}
	}
	if len(missingRequiredFields) > 0 {
		return nil, &ErrInvalidIssueFields{isRequiredErr: true, resources: missingRequiredFields}
	}

	// handle mapping type fields
	presetFields := loadDefaultPresetFields(session)
	for jiraField, mappingType := range mappingTypes {
		switch mappingType.Type {
		case "preset":
			presetVal, exists := presetFields[mappingType.Value]
			if exists {
				output[jiraField] = presetVal
				continue
			}
			// this attribute is applied a best effort and must not return in case the mapping is not available
			isConnTagsAttr := strings.HasPrefix(mappingType.Value, "session.connection_tags")
			if !isConnTagsAttr {
				invalidPresetFields = append(invalidPresetFields, fmt.Sprintf("%q", mappingType.Value))
			}
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

func loadDefaultPresetFields(s models.Session) map[string]string {
	script := string(s.BlobInput)
	if len(script) > defaultScriptCharMaxSize {
		script = script[0:defaultScriptCharMaxSize] + fmt.Sprintf(" ...[TRUNCATED %v]", len(script[defaultScriptCharMaxSize:]))
	}
	presetFields := map[string]string{
		"session.id":          s.ID,
		"session.user_email":  s.UserEmail,
		"session.user_id":     s.UserID,
		"session.user_name":   s.UserName,
		"session.type":        s.ConnectionType,
		"session.subtype":     s.ConnectionSubtype,
		"session.connection":  s.Connection,
		"session.status":      s.Status,
		"session.script":      "{code:shell}" + script + "{code}",
		"session.start_date":  s.CreatedAt.Format(time.RFC3339),
		"session.webapp_link": appconfig.Get().ApiURL() + "/sessions/" + s.ID,
	}
	for key, val := range s.ConnectionTags {
		presetKey := "session.connection_tags." + key
		presetFields[presetKey] = val
	}
	return presetFields
}

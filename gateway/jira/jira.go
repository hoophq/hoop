package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hoophq/hoop/common/log"
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

func CreateIssue(issueTemplate *models.JiraIssueTemplate, config *models.JiraIntegration, customFields CustomFields) (*IssueResponse, error) {
	if customFields == nil {
		return nil, fmt.Errorf("custom fields map is empty")
	}
	log.Infof("creating jira issue with fields: %v", customFields)
	issueFields := IssueFields[CustomFields]{
		Project:      Project{Key: issueTemplate.ProjectKey},
		Summary:      "Hoop Session",
		Issuetype:    Issuetype{Name: issueTemplate.IssueTypeName},
		CustomFields: customFields,
	}
	issuePayload, err := json.Marshal(map[string]any{"fields": issueFields})
	if err != nil {
		return nil, fmt.Errorf("failed encoding issue payload, reason=%v", err)
	}
	apiURL := fmt.Sprintf("%s/rest/api/3/issue", config.URL)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(issuePayload))
	if err != nil {
		return nil, fmt.Errorf("failed creating request, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(config.User, config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed creating jira issue, reason=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unable to create jira issue, status=%v, body=%v",
			resp.StatusCode, string(body))
	}
	var response IssueResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed decoding jira issue response, reason=%v", err)
	}
	return &response, nil
}

func ParseIssueFields(tmpl *models.JiraIssueTemplate, customFields CustomFields, session types.Session) (CustomFields, error) {
	if customFields == nil {
		customFields = CustomFields{}
	}
	newCustomFields := CustomFields{}
	mappingTypes, promptTypes, err := tmpl.DecodeMappingTypes()
	if err != nil {
		return nil, err
	}
	invalidPresetFields, missingRequiredFields := []string{}, []string{}
	for jiraField, promptType := range promptTypes {
		val, ok := customFields[jiraField]
		if !ok {
			if promptType.Required {
				missingRequiredFields = append(missingRequiredFields, fmt.Sprintf("%q", jiraField))
			}
			continue
		}
		newCustomFields[jiraField] = val
	}
	if len(missingRequiredFields) > 0 {
		return nil, &ErrInvalidIssueFields{isRequiredErr: true, resources: missingRequiredFields}
	}
	presetFields := loadDefaultPresetFields(session)
	for jiraField, mappingType := range mappingTypes {
		switch mappingType.Type {
		case "preset":
			presetVal, ok := presetFields[mappingType.Value]
			if !ok {
				invalidPresetFields = append(invalidPresetFields, fmt.Sprintf("%q", mappingType.Value))
				continue
			}
			newCustomFields[jiraField] = presetVal
		case "custom":
			newCustomFields[jiraField] = mappingType.Value
		default:
			log.Warnf("mapping type (%v) not found", mappingType.Type)
		}
	}
	if len(invalidPresetFields) > 0 {
		return nil, &ErrInvalidIssueFields{resources: invalidPresetFields}
	}
	return newCustomFields, nil
}

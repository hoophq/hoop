package jira

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseIssueFields(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    *models.JiraIssueTemplate
		input   map[string]string
		session types.Session
		want    CustomFields
		err     error
	}{
		{
			name: "it must parse all fields with success",
			tmpl: &models.JiraIssueTemplate{
				PromptTypes: map[string]any{
					"items": []any{
						map[string]any{
							"jira_field": "customfield_10052",
							"required":   true,
						},
					},
				},
				MappingTypes: map[string]any{
					"items": []any{
						map[string]any{
							"jira_field": "customfield_10050",
							"type":       "preset",
							"value":      "session.connection",
						},
						map[string]any{
							"jira_field": "customfield_10051",
							"type":       "custom",
							"value":      "my-custom-value",
						},
					},
				},
				CmdbTypes: map[string]any{
					"items": []any{
						map[string]any{
							"jira_field": "customfield_10199",
							"required":   true,
						},
						map[string]any{
							"jira_field": "customfield_10377",
							"required":   false,
						},
					},
				},
			},
			input: map[string]string{
				"customfield_10052": "my-prompt-value",
				"customfield_10199": "cmdb-value",
			},
			session: types.Session{Connection: "myconnection"},
			want: CustomFields{
				"customfield_10050": "myconnection",
				"customfield_10051": "my-custom-value",
				"customfield_10052": "my-prompt-value",
				"customfield_10199": []map[string]string{{"id": "cmdb-value"}},
			},
			err: nil,
		},
		{
			name: "it must return error when required fields are missing in prompt types",
			tmpl: &models.JiraIssueTemplate{
				PromptTypes: map[string]any{
					"items": []any{
						map[string]any{
							"jira_field": "customfield_10052",
							"required":   true,
						},
					},
				},
			},
			err: &ErrInvalidIssueFields{isRequiredErr: true, resources: []string{`"customfield_10052"`}},
		},
		{
			name: "it must return error when required fields are missing in cmdb types",
			tmpl: &models.JiraIssueTemplate{
				CmdbTypes: map[string]any{
					"items": []any{
						map[string]any{
							"jira_field": "customfield_10190",
							"required":   true,
						},
					},
				},
			},
			err: &ErrInvalidIssueFields{isRequiredErr: true, resources: []string{`"customfield_10190"`}},
		},
		{
			name: "it must return error when mapping types has invalid preset field",
			tmpl: &models.JiraIssueTemplate{
				MappingTypes: map[string]any{
					"items": []any{
						map[string]any{
							"jira_field": "customfield_10050",
							"type":       "preset",
							"value":      "invalid.preset.field",
						},
					},
				},
			},
			err: &ErrInvalidIssueFields{resources: []string{`"invalid.preset.field"`}},
		},
		{
			name: "it should return empty output when fields are not found",
			tmpl: &models.JiraIssueTemplate{},
			err:  nil,
			want: CustomFields{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIssueFields(tt.tmpl, tt.input, tt.session)
			if tt.err != nil {
				require.Error(t, err)
				assert.EqualError(t, err, tt.err.Error())
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

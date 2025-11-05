package apirunbooks

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

var connectionList = []string{"postgres-db", "redis-cache", "aws-prod", "gcp-backup", "azure-storage"}

func TestGetRunbookConnections(t *testing.T) {
	for _, tt := range []struct {
		msg string

		runbookRepository string
		runbookName       string
		userGroups        []string
		runbookRules      []models.RunbookRules

		connectionExpected []string
	}{
		{
			msg: "no runbook rules defined, return all connections",

			runbookRepository: "repo-1",
			runbookName:       "runbook-1.yaml",
			userGroups:        []string{"group-1"},
			runbookRules:      []models.RunbookRules{},

			connectionExpected: connectionList,
		},
		{
			msg: "user is admin, return all connections",

			runbookRepository: "repo-2",
			runbookName:       "runbook-2.yaml",
			userGroups:        []string{"admin"},
			runbookRules: []models.RunbookRules{
				{
					Name:       "rule-1",
					UserGroups: []string{"group-1"},
					Connections: []string{
						"postgres-db",
					},
				},
			},

			connectionExpected: connectionList,
		},
		{
			msg: "matching runbook rule found, return its connections",

			runbookRepository: "repo-3",
			runbookName:       "runbook-3.yaml",
			userGroups:        []string{"group-1"},
			runbookRules: []models.RunbookRules{
				{
					Name:       "rule-1",
					UserGroups: []string{"group-1"},
					Connections: []string{
						"postgres-db",
						"redis-cache",
					},
					Runbooks: []models.RunbookRuleFile{
						{
							Repository: "repo-3",
							Name:       "runbook-3.yaml",
						},
					},
				},
			},

			connectionExpected: []string{"postgres-db", "redis-cache"},
		},
		{
			msg: "no matching runbook rule found, return empty list",

			runbookRepository: "repo-4",
			runbookName:       "runbook-4.yaml",
			userGroups:        []string{"group-2"},
			runbookRules: []models.RunbookRules{
				{
					Name:       "rule-1",
					UserGroups: []string{"group-1"},
					Connections: []string{
						"aws-prod",
					},
					Runbooks: []models.RunbookRuleFile{
						{
							Repository: "repo-3",
							Name:       "runbook-3.yaml",
						},
					},
				},
			},

			connectionExpected: []string{},
		},
		{
			msg: "multiple matching runbook rules with connection, runbooks and group filled",

			runbookRepository: "repo-5",
			runbookName:       "runbook-5.yaml",
			userGroups:        []string{"group-1", "group-2"},
			runbookRules: []models.RunbookRules{
				{
					Name:       "rule-1",
					UserGroups: []string{"group-1"},
					Connections: []string{
						"aws-prod",
					},
					Runbooks: []models.RunbookRuleFile{
						{
							Repository: "repo-5",
							Name:       "runbook-5.yaml",
						},
					},
				},
				{
					Name:       "rule-2",
					UserGroups: []string{"group-2"},
					Connections: []string{
						"gcp-backup",
					},
					Runbooks: []models.RunbookRuleFile{
						{
							Repository: "repo-5",
							Name:       "runbook-5.yaml",
						},
					},
				},
			},
			connectionExpected: []string{"aws-prod", "gcp-backup"},
		},
		{
			msg: "matching runbook rule with empty runbooks and filled connections and groups",

			runbookRepository: "repo-6",
			runbookName:       "runbook-6.yaml",
			userGroups:        []string{"group-1"},
			runbookRules: []models.RunbookRules{
				{
					Name:       "rule-1",
					UserGroups: []string{"group-1"},
					Connections: []string{
						"azure-storage",
					},
					Runbooks: []models.RunbookRuleFile{},
				},
			},

			connectionExpected: []string{"azure-storage"},
		},
		{
			msg: "matching runbook rule with only filled groups",

			runbookRepository: "repo-6",
			runbookName:       "runbook-6.yaml",
			userGroups:        []string{"group-1"},
			runbookRules: []models.RunbookRules{
				{
					Name:        "rule-1",
					UserGroups:  []string{"group-1"},
					Connections: []string{},
					Runbooks:    []models.RunbookRuleFile{},
				},
			},

			connectionExpected: connectionList,
		},
		{
			msg: "matching runbook rule with filled groups and runbooks",

			runbookRepository: "repo-7",
			runbookName:       "runbook-7.yaml",
			userGroups:        []string{"group-1"},
			runbookRules: []models.RunbookRules{
				{
					Name:        "rule-1",
					UserGroups:  []string{"group-1"},
					Connections: []string{},
					Runbooks: []models.RunbookRuleFile{
						{
							Repository: "repo-7",
							Name:       "runbook-7.yaml",
						},
					},
				},
			},

			connectionExpected: connectionList,
		},
		{
			msg: "matching runbook a rule with empty connection and group and another with filled groups",

			runbookRepository: "repo-8",
			runbookName:       "runbook-8.yaml",
			userGroups:        []string{"engineering"},
			runbookRules: []models.RunbookRules{
				{
					Name:        "rule-1",
					UserGroups:  []string{},
					Connections: []string{},
					Runbooks: []models.RunbookRuleFile{
						{
							Repository: "repo-8",
							Name:       "runbook-8.yaml",
						},
					},
				},
				{
					Name:        "rule-2",
					UserGroups:  []string{"engineering"},
					Connections: []string{"redis-cache"},
					Runbooks: []models.RunbookRuleFile{
						{
							Repository: "repo-8",
							Name:       "runbook-8.yaml",
						},
					},
				},
			},

			connectionExpected: connectionList,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			connections := getRunbookConnections(tt.runbookRules, connectionList, tt.runbookRepository, tt.runbookName, tt.userGroups)

			assert.ElementsMatch(t, tt.connectionExpected, connections)
		})
	}
}

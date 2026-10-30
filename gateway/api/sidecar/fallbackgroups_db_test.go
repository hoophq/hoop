package apisidecar

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// An old build files every held level under the managed rule. Its groups come
// from the human levels, never from the request, so no group approves a level
// there that it cannot approve on a new build.
func TestTheManagedRuleGroupsComeFromTheHumanLevels(t *testing.T) {
	startStatusTestDB(t)
	org := uuid.MustParse(statusTestOrgID)
	requested := []string{"requested"}

	for _, tc := range []struct {
		name, approvers string
		want            []string
	}{
		{"the same groups on every human level",
			`{"high": {"type": "human", "groups": ["dba", "sre"]}, "medium": {"type": "human", "groups": ["sre", " dba"]}}`,
			[]string{"dba", "sre"}},
		{"an ai_agent level does not change the groups",
			`{"high": {"type": "human", "groups": ["dba"]}, "low": {"type": "ai_agent"}}`,
			[]string{"dba"}},
		{"different groups",
			`{"high": {"type": "human", "groups": ["dba"]}, "medium": {"type": "human", "groups": ["sre"]}}`,
			[]string{types.GroupAdmin}},
		{"a human level with no groups",
			`{"high": {"type": "human", "groups": ["dba"]}, "medium": {"type": "human"}}`,
			[]string{types.GroupAdmin}},
		{"ai_agent levels only",
			`{"high": {"type": "ai_agent"}, "medium": {"type": "ai_agent"}}`,
			[]string{types.GroupAdmin}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := "derived-" + uuid.NewString()[:8]
			spec := json.RawMessage(`{"high": "require_approval", "medium": "require_approval", "low": "require_approval",
				"approval_rule": "` + rule + `", "approvers": ` + tc.approvers + `}`)
			require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, org, rule, spec, &requested))
			got, err := services.AnalyzerApprovalReviewers(models.DB, org, rule)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, "the requested groups are ignored")
		})
	}

	// No approvers: the request names the groups, as on main.
	const bare = `{"high": "require_approval", "approval_rule": "bare-hold"}`
	require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, org, "bare-hold", json.RawMessage(bare), &requested))
	got, err := services.AnalyzerApprovalReviewers(models.DB, org, "bare-hold")
	require.NoError(t, err)
	assert.Equal(t, requested, got)
}

// An import stores the groups derived from the file's human levels.
func TestAnImportDerivesTheManagedRuleGroups(t *testing.T) {
	startStatusTestDB(t)
	sc := seedStatusSidecar(t, "import-groups")
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
		Analyzer: &daemon.LaneAnalyzerConfig{HighRisk: "require_approval", MediumRisk: "require_approval",
			Approvers: &daemon.LevelApprovers{
				High:   &daemon.LevelApprover{Type: analyzer.ApproverHuman, Groups: []string{"dba"}},
				Medium: &daemon.LevelApprover{Type: analyzer.ApproverHuman, Groups: []string{"dba"}},
			}},
	}}}
	_, items, err := services.SplitSidecarConfiguration(sc.Name, cfg, nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NoError(t, models.DB.Transaction(func(tx *gorm.DB) error {
		return services.ImportSidecarRulesTx(tx, statusTestOrgID, sc.ID, items)
	}))
	got, err := services.AnalyzerApprovalReviewers(models.DB, uuid.MustParse(statusTestOrgID), items[0].Name)
	require.NoError(t, err)
	assert.Equal(t, []string{"dba"}, got)
}

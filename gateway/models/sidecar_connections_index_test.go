package models_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const resourceNameIndex = "idx_connections_org_resource_name"

func TestSidecarMirrorWritesUseResourceNameIndex(t *testing.T) {
	startTestDB(t)
	// Enough rows that the planner does not scan the table whatever the index.
	execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype)
		SELECT ?, 'mirror-' || n, 'database', 'postgres' FROM generate_series(1, 5000) n`, testOrgID)
	execSQL(t, `INSERT INTO private.connections (org_id, name, resource_name, type, subtype)
		SELECT ?, 'mirror-' || n, 'mirror-' || n, 'database', 'postgres' FROM generate_series(1, 5000) n`, testOrgID)
	execSQL(t, `ANALYZE private.connections`)
	execSQL(t, `ANALYZE private.resources`)

	explain := func(t *testing.T, query string, args ...any) string {
		t.Helper()
		rows, err := models.DB.Raw(`EXPLAIN `+query, args...).Rows()
		require.NoError(t, err)
		defer rows.Close()
		var plan []string
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan = append(plan, line)
		}
		require.NoError(t, rows.Err())
		return strings.Join(plan, "\n")
	}

	const name = "mirror-42"
	plans := map[string]func(t *testing.T) string{
		"update resource": func(t *testing.T) string {
			return explain(t, models.SidecarMirrorResourceUpdateQuery, testOrgID, name, "database", "postgres", name)
		},
		"name in use": func(t *testing.T) string {
			return explain(t, models.SidecarMirrorNameInUseQuery, testOrgID, name, name)
		},
		"create resource": func(t *testing.T) string {
			return explain(t, models.SidecarMirrorResourceCreateQuery, testOrgID, name, "database", "postgres")
		},
		"delete orphan resource": func(t *testing.T) string {
			return explain(t, models.OrphanResourceDeleteQuery, testOrgID, name)
		},
	}
	for label, plan := range plans {
		t.Run(label, func(t *testing.T) {
			got := plan(t)
			assert.Contains(t, got, resourceNameIndex)
			assert.NotContains(t, got, "Seq Scan on connections")
		})
	}

	t.Run("without the index", func(t *testing.T) {
		execSQL(t, `DROP INDEX private.`+resourceNameIndex)
		for label, plan := range plans {
			assert.NotContains(t, plan(t), resourceNameIndex, label)
		}
	})
}

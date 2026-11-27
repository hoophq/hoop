package models_test

import (
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENG-1285: tag_selector narrows sessions to the connections whose current
// tags match, with the rules of the connection tag_selector.
func TestListSessionsTagSelectorAndConnectionPattern(t *testing.T) {
	startTestDB(t)

	tag := func(conn, key, val string) {
		t.Helper()
		execSQL(t, `INSERT INTO private.connection_tags (org_id, key, value) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, testOrgID, key, val)
		execSQL(t, `INSERT INTO private.connection_tags_association (org_id, tag_id, connection_id)
			SELECT ?, ct.id, c.id FROM private.connection_tags ct, private.connections c
			WHERE ct.org_id = ? AND ct.key = ? AND ct.value = ? AND c.org_id = ? AND c.name = ?`,
			testOrgID, testOrgID, key, val, testOrgID, conn)
	}
	for _, name := range []string{"pg-prd-orders", "pg-stg-orders", "pg-prd-billing"} {
		seedAdminConnection(t, name, name)
	}
	tag("pg-prd-orders", "env", "prd")
	tag("pg-prd-orders", "team", "data")
	tag("pg-stg-orders", "env", "stg")
	tag("pg-stg-orders", "team", "data")
	tag("pg-prd-billing", "env", "prd")
	tag("pg-prd-billing", "team", "finance")

	now := time.Now().UTC()
	seed := func(id, conn, input string, age time.Duration) string {
		t.Helper()
		require.NoError(t, models.UpsertSession(models.Session{
			ID:             id,
			OrgID:          testOrgID,
			Connection:     conn,
			ConnectionType: "database",
			Verb:           "exec",
			Status:         "done",
			BlobInput:      models.BlobInputType(input),
			CreatedAt:      now.Add(-age),
		}))
		return id
	}
	prdUpdate := seed("00000000-0000-0000-0000-000000000c01", "pg-prd-orders", "UPDATE orders SET paid = true", 5*time.Minute)
	prdSelect := seed("00000000-0000-0000-0000-000000000c02", "pg-prd-orders", "SELECT 1", 4*time.Minute)
	stgUpdate := seed("00000000-0000-0000-0000-000000000c03", "pg-stg-orders", "UPDATE orders SET paid = true", 3*time.Minute)
	billing := seed("00000000-0000-0000-0000-000000000c04", "pg-prd-billing", "UPDATE invoices SET sent = true", 2*time.Minute)
	// A session of a connection that no longer exists has no current tags.
	seed("00000000-0000-0000-0000-000000000c05", "pg-prd-deleted", "UPDATE orders SET paid = true", time.Minute)

	list := func(mutate func(*models.SessionOption)) ([]string, *int64, error) {
		t.Helper()
		opt := models.NewSessionOption()
		opt.CountMode = models.SessionCountExact
		mutate(&opt)
		got, err := models.ListSessions(testOrgID, "admin", true, opt)
		if err != nil {
			return nil, nil, err
		}
		ids := make([]string, 0, len(got.Items))
		for _, s := range got.Items {
			ids = append(ids, s.ID)
		}
		return ids, got.Total, nil
	}

	for _, tt := range []struct {
		msg    string
		mutate func(*models.SessionOption)
		want   []string
	}{
		{
			msg:    "one condition keeps the sessions of the tagged connections",
			mutate: func(o *models.SessionOption) { o.TagSelector = "env=prd" },
			want:   []string{billing, prdSelect, prdUpdate},
		},
		{
			msg:    "conditions are ANDed",
			mutate: func(o *models.SessionOption) { o.TagSelector = "env=prd, team=data" },
			want:   []string{prdSelect, prdUpdate},
		},
		{
			msg:    "!= needs the key with another value",
			mutate: func(o *models.SessionOption) { o.TagSelector = "env!=prd" },
			want:   []string{stgUpdate},
		},
		{
			msg:    "a tag no connection has matches nothing",
			mutate: func(o *models.SessionOption) { o.TagSelector = "env=dev" },
			want:   []string{},
		},
		{
			msg:    "blank segments are no filter",
			mutate: func(o *models.SessionOption) { o.TagSelector = " , " },
			want:   []string{"00000000-0000-0000-0000-000000000c05", billing, stgUpdate, prdSelect, prdUpdate},
		},
		{
			msg:    "composes with input",
			mutate: func(o *models.SessionOption) { o.TagSelector, o.Input = "env=prd", "update" },
			want:   []string{billing, prdUpdate},
		},
		{
			msg:    "the connection filter takes a LIKE pattern",
			mutate: func(o *models.SessionOption) { o.ConnectionName = "%-prd-%" },
			want:   []string{"00000000-0000-0000-0000-000000000c05", billing, prdSelect, prdUpdate},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			ids, total, err := list(tt.mutate)
			require.NoError(t, err)
			assert.Equal(t, tt.want, ids)
			require.NotNil(t, total)
			assert.EqualValues(t, len(tt.want), *total, "the count must describe the same rows as the page")
		})
	}

	t.Run("non-admin visibility still applies", func(t *testing.T) {
		opt := models.NewSessionOption()
		opt.TagSelector = "env=prd"
		got, err := models.ListSessions(testOrgID, "someone-else", false, opt)
		require.NoError(t, err)
		assert.Empty(t, got.Items)
	})

	t.Run("an invalid selector is an error", func(t *testing.T) {
		_, _, err := list(func(o *models.SessionOption) { o.TagSelector = "env" })
		assert.ErrorContains(t, err, "invalid tag selector")
		_, _, err = list(func(o *models.SessionOption) { o.TagSelector = "env=\x00" })
		assert.ErrorContains(t, err, "without NUL bytes", "a NUL must not reach the database")
	})
}

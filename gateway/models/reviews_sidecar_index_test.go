package models_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// captureQuery runs fn and returns the SQL and the args of the query it ran.
func captureQuery(t *testing.T, fn func()) (string, []any) {
	t.Helper()
	const name = "test:capture_query"
	var query string
	var args []any
	err := models.DB.Callback().Query().After("gorm:query").Register(name, func(db *gorm.DB) {
		query, args = db.Statement.SQL.String(), db.Statement.Vars
	})
	if err != nil {
		t.Fatalf("register the capture: %v", err)
	}
	defer func() { _ = models.DB.Callback().Query().Remove(name) }()
	fn()
	if query == "" {
		t.Fatal("no query ran")
	}
	return query, args
}

// sqlLiteral renders an EXECUTE argument; PREPARE gave each one its type.
func sqlLiteral(v any) string {
	if tm, ok := v.(time.Time); ok {
		v = tm.UTC().Format("2006-01-02 15:04:05.999999")
	}
	return "'" + strings.ReplaceAll(fmt.Sprint(v), "'", "''") + "'"
}

// The live lookup of each key uses that key's partial unique index, on a
// custom and on a generic plan. A predicate the index predicate does not
// imply (IS NOT DISTINCT FROM, a bound status) leaves a seq scan of every
// review, on each filing. enable_seqscan off makes the check independent of
// the row count: the planner takes the index whenever it can.
func TestTheLiveSidecarLookupUsesThePartialIndexes(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "index-lookup")
	hash := models.HashStatement([]byte("DELETE FROM t"))

	for _, tc := range []struct {
		key   models.SidecarReviewKey
		index string
	}{
		{models.SidecarReviewKey{RuleName: "payments-approvers"}, "index_reviews_sidecar_statement"},
		{models.SidecarReviewKey{RiskLevel: "high"}, "index_reviews_sidecar_level_statement"},
	} {
		query, args := captureQuery(t, func() {
			_, _ = models.GetLiveSidecarReview(models.DB, testOrgID, sc.ID, "appdb", tc.key, hash, time.Now().UTC())
		})
		literals := make([]string, len(args))
		for i, a := range args {
			literals[i] = sqlLiteral(a)
		}
		for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
			var plan []string
			err := models.DB.Transaction(func(tx *gorm.DB) error {
				for _, stmt := range []string{
					"SET LOCAL enable_seqscan = off",
					"SET LOCAL plan_cache_mode = " + mode,
					"PREPARE live_lookup AS " + query,
				} {
					if err := tx.Exec(stmt).Error; err != nil {
						return err
					}
				}
				defer tx.Exec("DEALLOCATE live_lookup")
				return tx.Raw("EXPLAIN EXECUTE live_lookup(" + strings.Join(literals, ", ") + ")").Scan(&plan).Error
			})
			if err != nil {
				t.Fatalf("%+v %s: explain: %v", tc.key, mode, err)
			}
			text := strings.Join(plan, "\n")
			if !strings.Contains(text, tc.index+" ") {
				t.Errorf("%+v %s: the lookup does not use %s:\n%s", tc.key, mode, tc.index, text)
			}
		}
	}
}

package models_test

import (
	"errors"
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
// the row count: the planner takes the index whenever it can. A level key
// uses the 000148 index, which keys on via_handoff: the flag is an index
// condition, not a filter. Over an empty table the sidecar_id index ties on
// cost with that six-column index, so the check drops it in a transaction it
// rolls back.
func TestTheLiveSidecarLookupUsesThePartialIndexes(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "index-lookup")
	hash := models.HashStatement([]byte("DELETE FROM t"))

	for _, tc := range []struct {
		key       models.SidecarReviewKey
		index     string
		indexCond string
	}{
		{models.SidecarReviewKey{RuleName: "payments-approvers"}, "index_reviews_sidecar_statement", "access_request_rule_name ="},
		{models.SidecarReviewKey{RiskLevel: "high"}, "index_reviews_sidecar_level_statement", "via_handoff ="},
		{models.SidecarReviewKey{RiskLevel: "high", ViaHandoff: true}, "index_reviews_sidecar_level_statement", "via_handoff ="},
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
					"DROP INDEX private.index_reviews_sidecar_id",
					"PREPARE live_lookup AS " + query,
				} {
					if err := tx.Exec(stmt).Error; err != nil {
						return err
					}
				}
				defer tx.Exec("DEALLOCATE live_lookup")
				if err := tx.Raw("EXPLAIN EXECUTE live_lookup(" + strings.Join(literals, ", ") + ")").Scan(&plan).Error; err != nil {
					return err
				}
				return errRollback
			})
			if !errors.Is(err, errRollback) {
				t.Fatalf("%+v %s: explain: %v", tc.key, mode, err)
			}
			text := strings.Join(plan, "\n")
			if !strings.Contains(text, tc.index+" ") {
				t.Errorf("%+v %s: the lookup does not use %s:\n%s", tc.key, mode, tc.index, text)
			}
			if !indexCondHas(plan, tc.indexCond) {
				t.Errorf("%+v %s: %q is not an index condition:\n%s", tc.key, mode, tc.indexCond, text)
			}
		}
	}
}

// errRollback ends a transaction whose changes the check must not keep.
var errRollback = errors.New("roll back")

// indexCondHas reports an Index Cond line of plan that holds cond.
func indexCondHas(plan []string, cond string) bool {
	for _, line := range plan {
		if strings.Contains(line, "Index Cond:") && strings.Contains(line, cond) {
			return true
		}
	}
	return false
}

// A rule filing carries no hand-off flag: a rule key with one is refused,
// not matched against a scope no index covers.
func TestARuleKeyWithAHandoffIsRefused(t *testing.T) {
	startTestDB(t)
	sc := seedSidecar(t, "index-refused")
	_, err := models.GetLiveSidecarReview(models.DB, testOrgID, sc.ID, "appdb",
		models.SidecarReviewKey{RuleName: "payments-approvers", ViaHandoff: true},
		models.HashStatement([]byte("DELETE FROM t")), time.Now().UTC())
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("a rule key with a hand-off must be refused, err=%v", err)
	}
}

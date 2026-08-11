// Package conformance checks the hand-written scanner in lexer against
// PostgreSQL's own grammar.
//
// The scanner is not a parser and never will be. That is a defensible choice
// only while somebody keeps checking it, so PostgreSQL's real parser is the
// oracle and it runs on every `go test` here. It lives in a separate module
// because the root ships with zero dependencies.
package conformance

import (
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect/lexer"
	pg "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The property.
//
// For every statement, EITHER the scanner's write-set equals PostgreSQL's,
// OR the scanner reported Complete=false.
//
// The asymmetry is the point. A scanner that cannot match a real grammar for
// precision can still be sound, and soundness is what a policy engine needs:
// a write the scanner missed is a write the policy did not see. Conceding
// (Complete=false) fails the caller closed, so it is a correct outcome. What
// is never acceptable is a confident, different answer.
//
// Only the WRITE-set is asserted. The read-sets legitimately differ: the
// oracle sees `FROM cte_name` as a RangeVar because CTE resolution happens
// after parsing, while the scanner knows the name was bound by a WITH. Both
// are right about what they can see and neither difference can let a write
// through.

type stmtCase struct {
	group string
	sql   string
}

func TestScannerNeverContradictsPostgres(t *testing.T) {
	var exact, conceded int
	byGroup := map[string][2]int{}

	for _, c := range corpus() {
		t.Run(caseName(c.sql), func(t *testing.T) {
			got := lexer.Analyze(c.sql, lexer.Postgres)
			want, err := oracleWrites(c.sql)
			if err != nil {
				// A fixture PostgreSQL rejects is a broken fixture, not a
				// scanner finding. Fail loudly rather than skip quietly.
				t.Fatalf("oracle could not parse fixture: %v", err)
			}
			mine := scannerWrites(got)
			tally := byGroup[c.group]

			switch {
			// Order matters. A scanner that said Complete=false made no
			// claim, so it cannot be credited with agreeing even when the
			// write-sets happen to line up.
			case !got.Complete:
				conceded++
				tally[1]++
				t.Logf("conceded (%s): writes %v, postgres %v", got.Reason, mine, want)
			case slices.Equal(mine, want):
				exact++
				tally[0]++
			default:
				t.Errorf("scanner claims Complete=true and disagrees\n  sql:      %s\n  scanner:  %v\n  postgres: %v\n  verb=%s effects=%v",
					c.sql, mine, want, got.Verb, got.Effects)
			}
			byGroup[c.group] = tally
		})
	}

	total := len(corpus())
	t.Logf("agreement: %d/%d exact (%.1f%%), %d conceded, %d wrong",
		exact, total, 100*float64(exact)/float64(total), conceded, total-exact-conceded)
	for _, g := range groups() {
		tally := byGroup[g]
		t.Logf("  %-22s %2d exact, %d conceded", g, tally[0], tally[1])
	}
}

// scannerWrites reduces an Analysis to the sorted, lowercased set of relation
// names it says the statement changes.
//
// Lowercasing is what the root adapter does before a policy sees a name, so
// it is what the comparison must do. Both sides get it, so a quoted
// "Orders" folds the same way on both and the comparison stays sound.
func scannerWrites(a lexer.Analysis) []string {
	out := make([]string, 0, len(a.Relations))
	for _, r := range a.Relations {
		if r.Access == lexer.Write {
			out = append(out, strings.ToLower(r.Name))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func oracleWrites(sql string) ([]string, error) {
	tree, err := pgquery.Parse(sql)
	if err != nil {
		return nil, err
	}
	o := &oracle{rel: map[string]lexer.Access{}}
	for _, raw := range tree.GetStmts() {
		o.walk(raw.GetStmt())
	}
	out := make([]string, 0, len(o.rel))
	for name, acc := range o.rel {
		if acc == lexer.Write {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

// oracle collects (relation, read|write) out of a PostgreSQL parse tree.
//
// pg_query.Summary() would answer this directly, but wasilibs/go-pgquery does
// not re-export it (its pg_query.go exposes Scan, Parse, ParseToJSON,
// Deparse, ParsePlPgSqlToJSON, Normalize, Fingerprint and nothing else), and
// the pganalyze original that does is behind `//go:build cgo`. So the walk is
// here.
type oracle struct {
	rel map[string]lexer.Access

	// planOnly is the EXPLAIN-without-ANALYZE nesting depth. Inside it every
	// write demotes to a read, because EXPLAIN builds a plan and stops: the
	// DELETE it wraps touches the relation's statistics, not its rows. The
	// scanner models it the same way and this is where the two agree on
	// purpose rather than by accident.
	planOnly int
}

// walk dispatches on node type. Only the nodes that decide WRITE need a case;
// everything else descends generically, and a relation found on the way down
// is a read.
func (o *oracle) walk(m proto.Message) {
	if m == nil || reflectNil(m) {
		return
	}
	switch n := m.(type) {
	case *pg.RangeVar:
		o.touch(n, lexer.Read)

	// DML. The target relation is the write, the FROM/USING/source side is
	// the read, and the distinction is positional: only the grammar knows
	// which of `DELETE FROM a USING b` is which.
	case *pg.InsertStmt:
		o.touch(n.GetRelation(), lexer.Write)
		o.descend(n, "relation")
	case *pg.UpdateStmt:
		o.touch(n.GetRelation(), lexer.Write)
		o.descend(n, "relation")
	case *pg.DeleteStmt:
		o.touch(n.GetRelation(), lexer.Write)
		o.descend(n, "relation")
	case *pg.MergeStmt:
		// Every WHEN branch acts on MergeStmt.relation. sourceRelation and
		// the branch expressions are reads, so generic descent covers them.
		o.touch(n.GetRelation(), lexer.Write)
		o.descend(n, "relation")

	// A CTE body is reached by generic descent into CommonTableExpr.ctequery,
	// which lands on the DML cases above. That is the whole reason a
	// data-modifying CTE is visible here.

	case *pg.SelectStmt:
		// SELECT ... INTO t creates and fills t.
		o.touch(n.GetIntoClause().GetRel(), lexer.Write)
		o.descend(n, "into_clause")
	case *pg.CreateTableAsStmt:
		o.touch(n.GetInto().GetRel(), lexer.Write)
		o.descend(n, "into")

	// DDL. Creating, altering, indexing or refreshing a relation changes it.
	case *pg.CreateStmt:
		o.touch(n.GetRelation(), lexer.Write)
		o.descend(n, "relation")
	case *pg.AlterTableStmt:
		o.touch(n.GetRelation(), lexer.Write)
		o.descend(n, "relation")
	case *pg.IndexStmt:
		o.touch(n.GetRelation(), lexer.Write)
		o.descend(n, "relation")
	case *pg.ViewStmt:
		o.touch(n.GetView(), lexer.Write)
		o.descend(n, "view")
	case *pg.RefreshMatViewStmt:
		o.touch(n.GetRelation(), lexer.Write)
		o.descend(n, "relation")
	case *pg.TruncateStmt:
		for _, r := range n.GetRelations() {
			o.touch(r.GetRangeVar(), lexer.Write)
		}
		o.descend(n, "relations")
	case *pg.DropStmt:
		// DROP carries names as List-of-String, not RangeVar, because it
		// drops objects of many kinds through one node.
		if relationObject(n.GetRemoveType()) {
			for _, obj := range n.GetObjects() {
				o.name(dottedName(obj.GetList()), lexer.Write)
			}
			o.descend(n, "objects")
			return
		}
		o.descend(n)
	case *pg.GrantStmt:
		// GRANT rewrites the object's ACL. Nothing else about the relation
		// changes, but "who may read customers" is exactly the kind of change
		// a policy that guards customers cares about.
		if n.GetObjtype() == pg.ObjectType_OBJECT_TABLE {
			for _, obj := range n.GetObjects() {
				o.touch(obj.GetRangeVar(), lexer.Write)
			}
			o.descend(n, "objects")
			return
		}
		o.descend(n)

	case *pg.CopyStmt:
		// COPY t FROM loads rows in; COPY t TO reads them out.
		acc := lexer.Read
		if n.GetIsFrom() {
			acc = lexer.Write
		}
		o.touch(n.GetRelation(), acc)
		o.descend(n, "relation")

	case *pg.ExplainStmt:
		if !explainExecutes(n) {
			o.planOnly++
			o.descend(n)
			o.planOnly--
			return
		}
		o.descend(n)

	default:
		o.descend(m)
	}
}

// descend walks every message-valued field except the named ones, which the
// caller already consumed. Skipping by field NAME rather than listing the
// fields to visit means a field added by a future PostgreSQL is still walked,
// as a read, instead of silently dropped.
func (o *oracle) descend(m proto.Message, skip ...string) {
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		if slices.Contains(skip, string(fd.Name())) {
			return true
		}
		if fd.IsList() {
			l := v.List()
			for i := range l.Len() {
				o.walk(l.Get(i).Message().Interface())
			}
			return true
		}
		o.walk(v.Message().Interface())
		return true
	})
}

func (o *oracle) touch(rv *pg.RangeVar, acc lexer.Access) {
	if rv == nil {
		return
	}
	o.name(qualified(rv.GetCatalogname(), rv.GetSchemaname(), rv.GetRelname()), acc)
}

func (o *oracle) name(name string, acc lexer.Access) {
	if name == "" {
		return
	}
	if acc == lexer.Write && o.planOnly > 0 {
		acc = lexer.Read
	}
	if o.rel[name] == lexer.Write {
		return // write dominates read, same rule the scanner uses
	}
	o.rel[name] = acc
}

func qualified(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, strings.ToLower(p))
		}
	}
	return strings.Join(kept, ".")
}

func dottedName(l *pg.List) string {
	parts := make([]string, 0, len(l.GetItems()))
	for _, it := range l.GetItems() {
		parts = append(parts, it.GetString_().GetSval())
	}
	return qualified(parts...)
}

func relationObject(t pg.ObjectType) bool {
	switch t {
	case pg.ObjectType_OBJECT_TABLE, pg.ObjectType_OBJECT_VIEW,
		pg.ObjectType_OBJECT_MATVIEW, pg.ObjectType_OBJECT_FOREIGN_TABLE:
		return true
	}
	return false
}

// explainExecutes reports whether the EXPLAIN actually runs its statement.
// Only ANALYZE does, and only when it is not explicitly turned off.
func explainExecutes(n *pg.ExplainStmt) bool {
	for _, opt := range n.GetOptions() {
		d := opt.GetDefElem()
		if !strings.EqualFold(d.GetDefname(), "analyze") {
			continue
		}
		// `EXPLAIN ANALYZE` has no argument; `EXPLAIN (ANALYZE false)` has one.
		if arg := d.GetArg(); arg != nil {
			return boolArg(arg)
		}
		return true
	}
	return false
}

func boolArg(n *pg.Node) bool {
	if s := n.GetString_().GetSval(); s != "" {
		return !strings.EqualFold(s, "false") && !strings.EqualFold(s, "off") && s != "0"
	}
	return n.GetBoolean().GetBoolval() || n.GetInteger().GetIval() != 0
}

// reflectNil catches a typed-nil pointer arriving as a non-nil proto.Message,
// which happens whenever a getter returns a nil sub-message.
func reflectNil(m proto.Message) bool { return !m.ProtoReflect().IsValid() }

func caseName(sql string) string {
	name := strings.Join(strings.Fields(sql), " ")
	if len(name) > 72 {
		name = name[:72]
	}
	return strings.ReplaceAll(name, " ", "_")
}

func groups() []string {
	return []string{"regression", "cte", "dml-shapes", "ddl", "orm"}
}

// corpus is the statement set. Every row of the classifier's regression table
// is here, so a fix that re-breaks one of them fails against PostgreSQL and
// not just against our own expectations.
func corpus() []stmtCase {
	var out []stmtCase
	add := func(group string, sqls ...string) {
		for _, s := range sqls {
			out = append(out, stmtCase{group: group, sql: s})
		}
	}

	// The statements that motivated replacing the previous classifier. Each
	// one was misread before the rewrite; PostgreSQL is the arbiter now.
	add("regression",
		`UPDATE audit SET n=E'O\'Brien'; DELETE FROM customers`,
		`WITH a AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM a`,
		`WITH x AS (SELECT $$a)b$$) DELETE FROM customers`,
		`WITH set AS (SELECT 1) SELECT * FROM set`,
		`/* outer /* inner */ DELETE FROM customers */ SELECT 1`,
		`MERGE INTO customers c USING staging s ON c.id = s.id WHEN MATCHED THEN DELETE`,
		`COPY (DELETE FROM customers RETURNING *) TO STDOUT`,
		`EXPLAIN ANALYZE DELETE FROM customers`,
		`EXPLAIN DELETE FROM customers`,
		`INSERT INTO staging SELECT * FROM customers`,
		`DO $$ BEGIN DELETE FROM customers; END $$`,
		`CALL purge()`,
	)

	add("cte",
		`WITH x AS (SELECT 1), y AS (DELETE FROM t RETURNING *) SELECT * FROM x, y`,
		`WITH outer_q AS (WITH inner_q AS (DELETE FROM deep RETURNING *) SELECT * FROM inner_q) SELECT * FROM outer_q`,
		`WITH RECURSIVE r AS (SELECT 1 AS n UNION ALL SELECT n+1 FROM r WHERE n < 10) SELECT * FROM r`,
		`WITH moved AS (DELETE FROM live RETURNING *) INSERT INTO archive SELECT * FROM moved`,
		`WITH a AS (UPDATE t1 SET x = 1 RETURNING id), b AS (UPDATE t2 SET y = 2 RETURNING id) SELECT * FROM a JOIN b USING (id)`,
		`WITH s AS (SELECT id FROM src WHERE ok) UPDATE dst SET n = 1 FROM s WHERE dst.id = s.id`,
	)

	add("dml-shapes",
		`UPDATE a SET x = 1 FROM b WHERE a.id = b.id`,
		`UPDATE public."Orders" o SET n = s.n FROM staging s WHERE o.id = s.id`,
		`DELETE FROM public."Orders" o USING staging s WHERE o.id = s.id`,
		`DELETE FROM a USING b, c WHERE a.id = b.id AND b.k = c.k`,
		`SELECT * FROM a UNION SELECT * FROM b`,
		`SELECT * FROM a UNION ALL SELECT * FROM b EXCEPT SELECT * FROM c`,
		`INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO UPDATE SET a = 2`,
		`INSERT INTO t (a) VALUES (1) ON CONFLICT DO NOTHING`,
		`INSERT INTO t SELECT * FROM (SELECT * FROM u) z`,
		`CREATE TABLE snap AS SELECT * FROM customers`,
		`SELECT * INTO snap2 FROM customers`,
		`MERGE INTO customers c USING staging s ON c.id = s.id
		   WHEN MATCHED AND s.dead THEN DELETE
		   WHEN MATCHED THEN UPDATE SET n = s.n
		   WHEN NOT MATCHED THEN INSERT (id, n) VALUES (s.id, s.n)`,
		`SELECT * FROM "Customers" WHERE "Id" = 1`,
		`DELETE FROM warehouse.public.stock WHERE qty = 0`,
	)

	add("ddl",
		`CREATE TABLE t (id int PRIMARY KEY)`,
		`ALTER TABLE t ADD COLUMN c int`,
		`DROP TABLE IF EXISTS public.a, b`,
		`TRUNCATE TABLE a, b`,
		`CREATE INDEX i ON t (c)`,
		`CREATE VIEW v AS SELECT * FROM t`,
		`REFRESH MATERIALIZED VIEW mv`,
		`GRANT SELECT ON customers TO app`,
		`REVOKE INSERT ON customers FROM app`,
		`GRANT ALL ON warehouse.stock, warehouse.audit TO app`,
		// COPY is one keyword with two directions. TO is an export, so it
		// reads; a rule watching reads of customers has to see
		// `COPY customers TO PROGRAM 'curl ...'` as a read of customers, not
		// as a write it happens to also flag.
		`COPY t FROM STDIN`,
		`COPY t (a, b) FROM STDIN`,
		`COPY t TO STDOUT`,
		`COPY t (a, b) TO STDOUT WITH CSV HEADER`,
		`COPY public.t TO PROGRAM 'sink'`,
		`COPY (SELECT * FROM t) TO STDOUT`,
	)

	// What an ORM actually emits. If the scanner is wrong here it is wrong on
	// the traffic it will see all day.
	add("orm",
		`SELECT "users"."id", "users"."email" FROM "users" WHERE "users"."id" = $1 LIMIT 1`,
		`SELECT * FROM users ORDER BY id DESC LIMIT 20 OFFSET 40`,
		`SELECT count(*) FROM orders WHERE status = $1`,
		`SELECT u.*, p.* FROM users u INNER JOIN profiles p ON p.user_id = u.id WHERE u.id = $1`,
		`SELECT * FROM users u LEFT JOIN orders o ON o.user_id = u.id LEFT JOIN items i ON i.order_id = o.id`,
		`SELECT * FROM a WHERE id IN (SELECT id FROM b WHERE k = $1)`,
		`SELECT EXISTS (SELECT 1 FROM sessions WHERE token = $1)`,
		`SELECT id, sum(total) FROM orders GROUP BY id HAVING sum(total) > $1`,
		`SELECT * FROM orders WHERE created_at BETWEEN $1 AND $2 FOR UPDATE`,
		`SELECT DISTINCT ON (user_id) * FROM events ORDER BY user_id, at DESC`,
		`SELECT row_to_json(t) FROM (SELECT * FROM users) t`,
		`INSERT INTO users (name, email) VALUES ($1, $2) RETURNING id`,
		`INSERT INTO users (name) VALUES ($1), ($2), ($3)`,
		`UPDATE users SET name = $1, updated_at = now() WHERE id = $2 RETURNING *`,
		`UPDATE counters SET n = n + 1 WHERE k = $1`,
		`DELETE FROM sessions WHERE expires_at < now()`,
		`DELETE FROM users WHERE id = ANY($1::bigint[])`,
		`SELECT tags[1] FROM posts WHERE id = $1`,
		`SELECT data->>'name' FROM documents WHERE data @> $1::jsonb`,
		`SELECT * FROM pg_catalog.pg_class WHERE relname = $1`,
		`SELECT nextval('users_id_seq')`,
		`BEGIN`,
		`COMMIT`,
		`SET search_path TO public`,
		`SELECT * FROM generate_series(1, 10) g`,
		`SELECT to_char(created_at, 'YYYY-MM') AS m, count(*) FROM orders GROUP BY 1 ORDER BY 1`,
	)

	return out
}

// TestCorpusGroupsAreCovered keeps the group labels and the corpus honest: a
// typo in a group name would silently drop a slice out of the summary.
func TestCorpusGroupsAreCovered(t *testing.T) {
	seen := map[string]int{}
	for _, c := range corpus() {
		seen[c.group]++
	}
	if n := seen["orm"]; n < 20 {
		t.Errorf("orm group has %d statements, want at least 20", n)
	}
	for _, g := range groups() {
		if seen[g] == 0 {
			t.Errorf("group %q is declared but empty", g)
		}
		delete(seen, g)
	}
	for g := range seen {
		t.Errorf("group %q is used by the corpus but missing from groups()", g)
	}
}

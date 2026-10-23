package lexer

import (
	"slices"
	"strings"
)

// Access says whether a statement reads a relation or changes it.
//
// The previous implementation could not make this distinction. Without it,
// "nothing writes to customers" and "nothing touches customers" are the same
// rule, so `INSERT INTO staging SELECT * FROM customers` trips a write rule
// while `WITH x AS (DELETE FROM customers) SELECT` trips nothing.
type Access uint8

const (
	Read Access = iota
	Write
)

func (a Access) String() string {
	if a == Write {
		return "write"
	}
	return "read"
}

// Relation is one relation the statement touches, and how.
type Relation struct {
	Name   string
	Access Access
}

// Analysis is what one statement does.
type Analysis struct {
	// Verb is the leading statement verb as written. It answers "what did
	// the user type", which is the wrong question for policy and the right
	// one for an audit record.
	Verb Verb

	// Effects is every operation the statement performs, anywhere in the
	// tree. For `WITH x AS (DELETE FROM t) SELECT` it holds both. This is
	// the field a policy asking "does this write" must read.
	Effects []Verb

	// Relations lists what the statement touches, deduplicated, with write
	// dominating read for a relation that is both.
	Relations []Relation

	// Complete reports whether the scan understood the whole statement.
	//
	// FALSE MUST FAIL CLOSED. Every other field is best-effort when this is
	// false, and the honest reading is "no idea", not "nothing found".
	Complete bool

	// Reason names what defeated the scan. Empty when Complete.
	Reason string
}

// Severity returns the most consequential effect, for a caller that needs one
// verb. Effects{select, delete} is a delete.
func (a Analysis) Severity() Verb {
	worst := a.Verb
	for _, e := range a.Effects {
		if e.severity() > worst.severity() {
			worst = e
		}
	}
	return worst
}

// Writes reports whether the statement changes anything.
func (a Analysis) Writes() bool {
	return slices.ContainsFunc(a.Effects, Verb.mutating)
}

// Names returns the relation names, order preserved. It backs the legacy
// flat Tables view.
func (a Analysis) Names() []string {
	out := make([]string, 0, len(a.Relations))
	for _, r := range a.Relations {
		out = append(out, r.Name)
	}
	return out
}

type regionKind uint8

const (
	regTop regionKind = iota
	regCTE
	regSub
	regParen
)

// region is one level of nesting, LABELLED.
//
// The label is the entire difference from a depth counter. Knowing you are at
// depth 1 cannot tell a CTE body from a function argument list; knowing the
// paren was opened by `AS` in a WITH list can.
type region struct {
	kind regionKind

	// verb governs relation attribution inside this region. It changes when
	// a nested statement head appears, so `CREATE TABLE x AS SELECT ... FROM
	// y` attributes x to the create and y to the select.
	verb Verb

	// firstTarget tracks whether this region has claimed its write target
	// yet. `DELETE FROM a USING b` writes a and reads b, and only position
	// distinguishes them.
	firstTarget bool
}

type analyzer struct {
	toks []Token
	d    Dialect

	stack   []region
	effects []Verb
	rels    []Relation

	// cteNames are bound CTE aliases. A relation matching one is not a base
	// relation, so a `tables: [x]` rule stops matching someone's CTE and a
	// table list stops reporting aliases as real objects.
	cteNames map[string]bool

	// inCTEList is true between WITH and the statement that follows it.
	// It exists so the CTE NAME is never tested against the verb table:
	// without it `WITH set AS (SELECT 1) SELECT` classifies as a set.
	inCTEList bool

	// expectCTEName is true where the next word names a CTE.
	expectCTEName bool

	// explainSeen / analyzeSeen implement the one case where a wrapper
	// changes whether effects happen at all: EXPLAIN plans, EXPLAIN ANALYZE
	// executes.
	explainSeen bool
	analyzeSeen bool

	// wrapper is true between EXPLAIN and the statement it wraps, so head
	// position survives the option list.
	wrapper      bool
	sawStatement bool

	// plsql is true once an Oracle BEGIN or DECLARE opened an anonymous
	// block. From there DML is recognised anywhere, not only in head
	// position, and INTO names variables rather than tables; see plsqlDML.
	plsql bool

	// script is plsql's Spark counterpart: true once BEGIN opened a SQL
	// script. From there DML is recognised anywhere, as in PL/SQL; see
	// sparkScriptDML.
	script bool

	// fromFirst is true in a Spark statement that opened with FROM, the
	// Hive form `FROM s INSERT INTO t SELECT ... INSERT INTO u SELECT ...`.
	// Its INSERT and SELECT follow a relation, not a head position.
	fromFirst bool

	incomplete string
}

// Analyze reports what one SQL statement does.
//
// It never returns an error. A statement it cannot follow comes back with
// Complete=false and a Reason, because a caller on a data path needs a
// verdict rather than an error to log.
func Analyze(sql string, d Dialect) Analysis {
	switch d {
	case MySQL:
		return analyzeMySQL(sql)
	case Spark:
		return analyzeSpark(sql)
	}
	return analyzeWith(sql, d.rules(), d)
}

// analyzeMySQL reads the statement under BOTH backslash conventions and
// merges the results.
//
// Whether `\` escapes inside '...' is a per-session setting: it is on by
// default and off under NO_BACKSLASH_ESCAPES, which any client may set for
// itself with one statement. The two readings disagree about where a literal
// ENDS, and therefore about how many statements the payload contains:
//
//	SET sql_mode='NO_BACKSLASH_ESCAPES';
//	SELECT 'a\'; DELETE FROM orders; -- '
//
// With escapes on, that is one SELECT whose literal swallows the delete.
// With them off, the literal ends at the first quote and the DELETE RUNS —
// verified against MySQL 8.4, where the row count dropped.
//
// The classifier cannot know the mode: it is handed a statement and a
// protocol, not a connection, and the mode can change mid-session. Picking
// either reading alone is a coin flip that fails open half the time. So both
// are scanned and their effects and relations UNIONED, which is the
// pessimistic answer: a statement is refused if EITHER reading finds
// something a rule refuses.
//
// The cost is a possible false denial on a statement that is harmless under
// the mode actually in force. That is the survivable direction, and it only
// arises for a literal containing a backslash immediately before a quote —
// rare outside a deliberate bypass.
func analyzeMySQL(sql string) Analysis {
	escaped := MySQL.rules()
	literal := escaped
	literal.backslashInPlainString = false

	a := analyzeWith(sql, escaped, MySQL)
	b := analyzeWith(sql, literal, MySQL)
	return mergeAnalyses(a, b)
}

// analyzeSpark reads the statement with Spark's lexer and, where another
// lexer that runs Spark text reads it differently, with that one too, and
// merges the readings as analyzeMySQL does: effects and relations unioned,
// verb and completeness from the first.
//
// Released Spark reads a `/*+` inside a block comment as plain text, where
// Spark master and Databricks SQL 2026.42 open a nested comment on it
// (SPARK-59536). `/* /*+ */ DROP TABLE t -- */` is all comment to the second
// and a DROP to the first, so text holding `/*+` is read both ways.
//
// Delta's parser sees every statement before Spark and keeps the ones its
// grammar names — VACUUM, OPTIMIZE, RESTORE and the rest of deltaCommand.
// Its lexer differs from Spark's in where comments END: flat block comments,
// no backslash-newline continuation, `/*+` as an ordinary comment. So the
// same bytes can be a comment to one and a command to the other:
//
//	/* /* */ VACUUM t RETAIN 0 HOURS -- */
//
// is all comment to Spark, which nests, and a VACUUM to Delta, which does
// not. Either reading alone misses a statement one of the two parsers runs.
//
// The Delta reading counts only when it begins with a Delta command. Delta
// hands any other statement to Spark, which the first reading models, and a
// flat reading of an ordinary statement would only add effects nothing runs:
// a SELECT whose comment line ends in a backslash would be denied for the
// DELETE Spark reads as comment.
//
// Databricks SQL 2026.42 does not reproduce the example: it lexes Delta
// commands with Spark's rules, and the text above fails there as an empty
// statement. Open-source Delta Lake (DeltaSqlParser) reads it flat, and on a
// warehouse the extra reading only flags text that cannot run.
func analyzeSpark(sql string) Analysis {
	spark := analyzeWith(sql, Spark.rules(), Spark)
	// Without a `/*+` in the text the two Spark lexers read the same tokens.
	if strings.Contains(sql, "/*+") {
		released := Spark.rules()
		released.hintOpensNestedComment = false
		spark = mergeReading(spark, analyzeWith(sql, released, Spark))
	}
	delta := newAnalyzer(sql, deltaRules(), Spark)
	if len(delta.toks) == 0 || delta.toks[0].Kind != Word || !deltaCommand[delta.toks[0].Text] {
		return spark
	}
	delta.walk()
	return mergeReading(spark, delta.result())
}

// mergeReading merges another lexer's reading of Spark text as mergeAnalyses
// does, and keeps that reading's incompleteness when it found an effect the
// first reading did not. The statement it could not read in full is then
// one the first reading never saw: `/* /*+ */ EXECUTE IMMEDIATE '...' -- */`
// is all comment to Spark master and dynamic SQL to released Spark, and
// reporting it complete would let it past a rule naming unknown.
func mergeReading(primary, alt Analysis) Analysis {
	out := mergeAnalyses(primary, alt)
	if out.Complete && !alt.Complete && len(out.Effects) > len(primary.Effects) {
		out.Complete, out.Reason = false, alt.Reason
	}
	return out
}

// analyzeWith runs one pass with an explicit rule set.
func analyzeWith(sql string, rules lexRules, d Dialect) Analysis {
	a := newAnalyzer(sql, rules, d)
	a.walk()
	return a.result()
}

// newAnalyzer scans sql and returns an analyzer ready to walk the tokens.
func newAnalyzer(sql string, rules lexRules, d Dialect) *analyzer {
	toks, bad := scanWith(sql, rules)
	return &analyzer{
		toks:       toks,
		d:          d,
		stack:      []region{{kind: regTop, verb: Unknown}},
		cteNames:   map[string]bool{},
		incomplete: bad,
	}
}

// mergeAnalyses unions two readings of the same statement, pessimistically.
//
// Verb and completeness come from the PRIMARY reading — the one the server
// uses unless the session changed its mode. Every effect and relation the
// alternate reading found is added, because those are what a rule matches
// on and missing one is a bypass.
//
// The alternate's INCOMPLETENESS is deliberately not propagated. That
// reading is speculative: it exists to reveal statements the primary one
// would swallow, and it routinely ends mid-literal on input the primary
// reads cleanly — `SELECT 'a\'; DELETE FROM t; -- '` leaves a dangling
// quote under the no-escape rules. Treating that as ambiguity would report
// almost every backslash literal as unreadable and deny it, which is a
// different failure from the one this guards against and a far more common
// one. What survives is the DELETE it found, which is the point.
func mergeAnalyses(primary, alt Analysis) Analysis {
	out := primary

	for _, e := range alt.Effects {
		if !slices.Contains(out.Effects, e) {
			out.Effects = append(out.Effects, e)
		}
	}

	for _, r := range alt.Relations {
		i := slices.IndexFunc(out.Relations, func(x Relation) bool {
			return x.Name == r.Name
		})
		switch {
		case i < 0:
			out.Relations = append(out.Relations, r)
		case r.Access == Write:
			// Write dominates: a relation read under one reading and
			// written under the other is written.
			out.Relations[i].Access = Write
		}
	}
	return out
}

func (a *analyzer) top() *region { return &a.stack[len(a.stack)-1] }

func (a *analyzer) fail(why string) {
	if a.incomplete == "" {
		a.incomplete = why
	}
}

func (a *analyzer) walk() {
	atHead := true

	// The loop reassigns i when a relation spans several tokens, so it
	// stays in the classic form.
	for i := 0; i < len(a.toks); i++ {
		t := a.toks[i]

		// An Oracle label, <<name>>, comes before a block or a loop and
		// leaves the head position as it was: `<<x>> BEGIN DELETE ...`
		// is the block BEGIN opens.
		if a.d == Oracle && a.puncts(i, "<") && a.puncts(i+1, "<") && i+2 < len(a.toks) &&
			a.toks[i+2].isName() && a.puncts(i+3, ">") && a.puncts(i+4, ">") {
			i += 4
			continue
		}

		if t.Kind == Punct {
			switch t.Text {
			case "(":
				a.push(i)
				atHead = a.top().kind == regCTE || a.top().kind == regSub || a.wrapper
				continue
			case ")":
				// A closed CTE body means the next thing is either
				// another CTE name after a comma or the statement the
				// WITH was a prefix to. Both are head positions, and
				// treating them as ordinary tokens is what let
				// `WITH x AS (...) DELETE` read as a select.
				wasCTE := a.top().kind == regCTE
				a.pop()
				atHead = wasCTE || a.wrapper
				continue
			case ";":
				if len(a.stack) != 1 {
					a.fail("unbalanced parentheses at statement end")
					a.stack = a.stack[:1]
				}
				*a.top() = region{kind: regTop, verb: Unknown}
				a.inCTEList, a.expectCTEName, a.fromFirst = false, false, false
				atHead = true
				continue
			case ",":
				if a.inCTEList && len(a.stack) == 1 {
					a.expectCTEName = true
				}
				atHead = a.wrapper
				continue
			case "@":
				// A GoogleSQL hint — @{FORCE_INDEX=i} glued to a
				// relation, or @{USE_ADDITIONAL_PARALLELISM=TRUE}
				// before the whole statement — is advice to the
				// optimizer, not an effect. The group is skipped at
				// TOKEN level, so a quoted value containing '}' was
				// already consumed as a literal, and atHead is left
				// exactly as it was: a statement-level hint must not
				// cost the SELECT after it its head position, or the
				// statement classifies as nothing and a lane that
				// forwards on `select` refuses it. Elsewhere '@' falls
				// through below: a bare @param binding is ordinary
				// punctuation in every dialect.
				if a.d == GoogleSQL && i+1 < len(a.toks) &&
					a.toks[i+1].Kind == Punct && a.toks[i+1].Text == "{" {
					j := i + 2
					for j < len(a.toks) && !(a.toks[j].Kind == Punct && a.toks[j].Text == "}") {
						j++
					}
					if j == len(a.toks) {
						// Half a hint means half a statement; what the
						// missing brace would have preceded is unknown.
						a.fail("unterminated statement hint")
					}
					i = j
					continue
				}
			}
			atHead = a.wrapper
			continue
		}

		if t.Kind != Word {
			atHead = a.wrapper
			continue
		}

		// A CTE name is bound, never classified. Without this the name
		// is looked up in the verb table and `WITH set AS (SELECT 1)`
		// classifies as a set.
		if a.expectCTEName {
			a.cteNames[t.Text] = true
			a.expectCTEName = false
			atHead = false
			continue
		}

		if t.Text == "with" {
			a.sawStatement = true
			atHead = false
			// Oracle's WITH FUNCTION / WITH PROCEDURE declares PL/SQL
			// that the query runs: procedural code, like a block.
			// `WITH function AS (...)` and `WITH function (a) AS (...)`
			// are CTEs of that name: an inline unit has a name next.
			if a.d == Oracle && i+2 < len(a.toks) && a.toks[i+1].Kind == Word &&
				oracleInlinePLSQL[a.toks[i+1].Text] && a.toks[i+2].isName() && !a.toks[i+2].isWord("as") {
				a.fail("inline PL/SQL in WITH; body is procedural code")
				a.plsql = true
				continue
			}
			a.inCTEList = true
			a.expectCTEName = true
			continue
		}

		// EXPLAIN and its option list precede the real statement, so the
		// head position has to survive them.
		if a.wrapper && wrapperModifier[t.Text] {
			if t.Text == "analyze" {
				a.analyzeSeen = true
			}
			atHead = true
			continue
		}

		// A Spark SQL script. BEGIN opens one unless it is a transaction
		// start; see sparkTransaction. The block performs nothing itself;
		// its statements do, and control flow decides which of them run,
		// so the whole is incomplete as an Oracle block is. Without this,
		// BEGIN read as a transaction and the DROP after it was not a head:
		// `BEGIN DROP TABLE t; END` dropped a table on a Databricks SQL
		// warehouse as a begin and a commit.
		if a.d == Spark && atHead && t.Text == "begin" && !a.sparkTransactionAt(i) {
			a.script = true
			a.sawStatement = true
			a.fail("SQL script; control flow decides which of its statements run")
			continue
		}

		// Spark's FROM-first form names its source before any verb; its
		// INSERTs follow that relation. See anywhereDML.
		if a.d == Spark && atHead && t.Text == "from" && len(a.stack) == 1 {
			a.fromFirst = true
		}

		if (atHead || a.anywhereDML(t.Text, i)) && a.sparkStatementAt(i) && a.head(t, i) {
			// A stored PL/SQL unit carries its body inline. What
			// follows the header is source Oracle compiles and does
			// not run, so the scan ends here with the header read.
			if a.d == Oracle && !a.plsql && a.top().verb == Create {
				if end, ok := a.storedUnit(i); ok {
					i = end
					atHead = false
					continue
				}
			}
			// Several keywords are both a verb and a relation
			// introducer: UPDATE t, TRUNCATE t, COPY t, and Oracle's
			// RENAME t TO u. Consuming the head must not skip the
			// target they name.
			if relIntro[t.Text] || a.d == Oracle && t.Text == "rename" {
				if j, rels, ok := a.relationsAfter(i); ok {
					for _, rel := range rels {
						a.addRelation(rel)
					}
					i = j
				}
			} else if a.d == Spark {
				i = a.sparkTarget(i)
			}
			// `BEGIN DELETE FROM t`: the block keyword is a verb AND
			// the position before the block's first statement.
			atHead = a.plsql && plsqlHeadAfter[t.Text]
			continue
		}

		// A relation introducer claims the list that follows. Oracle's
		// `FOR UPDATE OF col` locks rows: the name after it is a column.
		// Spark's INSERT OVERWRITE [TABLE] t names its target after
		// OVERWRITE, where no INTO introduces it. Only right after INSERT:
		// elsewhere overwrite is a name, and `SELECT id overwrite FROM s`
		// read as an introducer took FROM for the table and lost s.
		if (relIntro[t.Text] || a.d == Spark && t.Text == "overwrite" && i > 0 && a.toks[i-1].isWord("insert")) &&
			!(a.d == Oracle && a.forUpdate(i)) {
			if j, rels, ok := a.relationsAfter(i); ok {
				for _, rel := range rels {
					a.addRelation(rel)
				}
				i = j
			}
			atHead = false
			continue
		}

		atHead = a.headFollows(t.Text)
	}

	if len(a.stack) != 1 {
		a.fail("unbalanced parentheses")
	}
}

// headFollows reports whether a statement verb may begin after this keyword.
//
// Every entry in headAfter is unconditional except AS, which is one keyword in
// two unrelated roles. It introduces a statement in `CREATE TABLE x AS SELECT`
// and `PREPARE p AS SELECT`, and it introduces a COLUMN OR TABLE ALIAS
// everywhere else — and PostgreSQL allows a reserved word as an alias exactly
// when AS is written, so `SELECT 1 AS delete` is a legal select. Treating that
// `delete` as a statement head made the select a delete for every policy.
//
// Not hypothetical. Metabase's schema sync asks the catalog which privileges
// it holds and names each column after the privilege it tested:
//
//	select ... has_table_privilege(..., 'delete') as delete from pg_tables
//
// Against a read-only lane that was refused, and the sync failed on every
// table. Any BI tool introspecting privileges writes some version of it.
//
// The role is decided by the statement the AS sits in: only a DDL head takes a
// statement after AS. A CTE body needs nothing here — its opening parenthesis
// is labelled regCTE by push, and that is what restores head position inside
// it.
func (a *analyzer) headFollows(word string) bool {
	if word == "as" {
		return ddlVerb(a.top().verb)
	}
	if a.plsql && plsqlHeadAfter[word] {
		return true
	}
	if a.script && sparkScriptHeadAfter[word] {
		return true
	}
	return headAfter[word]
}

// anywhereDML reports whether the word at i begins a statement outside head
// position.
//
// Inside an Oracle PL/SQL block, see plsqlDML, and inside a Spark SQL script,
// sparkScriptDML. In Spark's FROM-first form, `FROM s INSERT INTO t SELECT a
// INSERT INTO u SELECT b`, every INSERT and SELECT at the top level is a
// statement head behind the relation or the clause before it.
func (a *analyzer) anywhereDML(word string, i int) bool {
	switch {
	case a.plsql:
		return plsqlDML[word] && !a.plsqlNotDML(i)
	case a.script:
		return sparkScriptDML[word] && !a.plsqlNotDML(i) && !a.sparkNotDML(i)
	case a.fromFirst:
		return (word == "insert" || word == "select") && len(a.stack) == 1
	}
	return false
}

// sparkTransactionAt reports whether the Spark BEGIN at i starts a
// transaction: TRANSACTION or WORK after it, and nothing but `;` after that.
// See sparkTransaction.
func (a *analyzer) sparkTransactionAt(i int) bool {
	return i+1 < len(a.toks) && a.toks[i+1].Kind == Word && sparkTransaction[a.toks[i+1].Text] &&
		(i+2 == len(a.toks) || a.puncts(i+2, ";"))
}

// sparkNotDML reports whether a DML word at i inside a Spark script is not a
// statement, as plsqlNotDML does for PL/SQL: SHOW CREATE TABLE reads a
// definition, and a MERGE neither INTO nor WITH a target is a join hint,
// `/*+ MERGE */`.
func (a *analyzer) sparkNotDML(i int) bool {
	switch a.toks[i].Text {
	case "create":
		return i > 0 && a.toks[i-1].isWord("show")
	case "merge":
		return !(i+1 < len(a.toks) && (a.toks[i+1].isWord("into") || a.toks[i+1].isWord("with")))
	}
	return false
}

// sparkStatementAt reports whether a verb Spark adds, at i, can begin its
// statement here, as plsqlNotDML rules out PL/SQL's known false readings.
// Seven of them have fixed keywords: REPLACE TABLE, LOAD DATA, CONVERT TO,
// MSCK REPAIR, REORG TABLE, REPAIR TABLE, and FOR two words after GENERATE
// (GENERATE mode FOR TABLE t, DeltaSqlBase.g4). replace(s, 'x', 'y') after
// a THEN, or a column called load or generate, has no such keyword there,
// and no statement Spark can run lacks it, so ruling them out hides no
// write. VACUUM, OPTIMIZE and RESTORE take a relation next, and any word can
// name one (`VACUUM over` parses on Databricks SQL), so they are always
// statements: a column with their name after THEN reads as a write, as a
// column called update does in every dialect.
func (a *analyzer) sparkStatementAt(i int) bool {
	word := a.toks[i].Text
	if _, ok := sparkVerb[word]; a.d != Spark || !ok {
		return true
	}
	var next Token
	if i+1 < len(a.toks) {
		next = a.toks[i+1]
	}
	switch word {
	case "replace", "reorg", "repair":
		return next.isWord("table")
	case "load":
		return next.isWord("data")
	case "convert":
		return next.isWord("to")
	case "msck":
		return next.isWord("repair")
	case "generate":
		return i+2 < len(a.toks) && a.toks[i+2].isWord("for")
	}
	return true
}

// push opens a region, labelling it from the token before the parenthesis.
func (a *analyzer) push(i int) {
	kind := regParen
	if prev, ok := a.prevWord(i); ok {
		switch {
		case prev == "as" && a.inCTEList:
			kind = regCTE
		case prev == "from" || prev == "join" || prev == "in" ||
			prev == "exists" || prev == "using" || prev == "copy":
			kind = regSub
		}
	}
	verb := Unknown
	if kind == regParen {
		// An expression group inherits its statement's verb so relations
		// inside a WHERE stay attributed to the right operation.
		verb = a.top().verb
	}
	a.stack = append(a.stack, region{kind: kind, verb: verb})
}

func (a *analyzer) pop() {
	if len(a.stack) == 1 {
		a.fail("unbalanced parentheses")
		return
	}
	closing := a.stack[len(a.stack)-1]
	a.stack = a.stack[:len(a.stack)-1]
	if closing.kind == regCTE {
		// The CTE body ended. Another name may follow a comma; anything
		// else means the main statement begins.
		a.expectCTEName = false
	}
}

// head handles a token in statement-head position. It reports whether the
// token was consumed as a verb.
func (a *analyzer) head(t Token, i int) bool {
	verb, ok := a.statementVerb(t.Text)
	if !ok {
		return false
	}
	a.sawStatement = true

	if why, bad := a.opaque(t.Text); bad {
		a.fail(why)
	}

	if a.d == Oracle {
		switch {
		case oracleOpaque[t.Text] != "":
			a.plsql = true
		case verb == Alter && i+1 < len(a.toks) && a.toks[i+1].isWord("session"):
			// ALTER SESSION is Oracle's spelling of SET: NLS formats,
			// CURRENT_SCHEMA, time zone — the PostgreSQL SET and
			// search_path this package already files under set. Every
			// driver may send one on connect, and filing it under
			// alter would make a lane refusing DDL refuse the login.
			// ALTER SYSTEM changes the instance and stays alter.
			verb = Set
		}
	}

	if verb == Explain {
		a.explainSeen = true
		a.wrapper = true
		// EXPLAIN does not execute what follows unless ANALYZE is given,
		// so the wrapper is recorded and the inner statement head is left
		// to the next iteration.
		return true
	}

	a.wrapper = false
	if a.top().kind == regTop {
		// The statement the WITH prefixed has begun, so a later `AS (`
		// is a subquery rather than another CTE body.
		a.inCTEList = false
	}
	r := a.top()
	r.verb = verb
	r.firstTarget = true
	a.effects = append(a.effects, verb)
	return true
}

// statementVerb looks a head keyword up under the analyzer's dialect.
func (a *analyzer) statementVerb(word string) (Verb, bool) {
	if a.d == Oracle {
		if oracleNotAVerb[word] {
			return Unknown, false
		}
		if v, ok := oracleVerb[word]; ok {
			return v, true
		}
	}
	if a.d == Spark {
		if sparkNotAVerb[word] {
			return Unknown, false
		}
		if v, ok := sparkVerb[word]; ok {
			return v, true
		}
	}
	v, ok := statementVerb[word]
	return v, ok
}

// sparkTarget records the relation a Spark or Delta command names straight
// after its keyword, where no introducer precedes it, and returns the index
// it consumed through. i is the command keyword.
//
// VACUUM t, OPTIMIZE t, RESTORE [TABLE] t and CONVERT TO DELTA t change t's
// files, so t is written; left alone, a rule fencing t never sees them.
// CACHE [LAZY] TABLE t and UNCACHE TABLE t change only what the cluster
// holds in memory, so t is read, where the TABLE introducer under an `other`
// verb would have written it. DESCRIBE [TABLE] [EXTENDED] t, and Delta's
// DESCRIBE HISTORY or DETAIL t, read t's definition, so t is read; without
// this, DESCRIBE TABLE EXTENDED t read a relation called extended. Any other
// keyword records nothing.
func (a *analyzer) sparkTarget(i int) int {
	acc, j := Write, i
	switch a.toks[i].Text {
	case "vacuum", "optimize", "restore":
	case "convert":
		if !(i+2 < len(a.toks) && a.toks[i+1].isWord("to") && a.toks[i+2].isWord("delta")) {
			return i
		}
		j = i + 2
	case "cache", "uncache":
		acc = Read
		if i+1 < len(a.toks) && a.toks[i+1].isWord("lazy") {
			j = i + 1
		}
	case "describe", "desc":
		acc = Read
		for j+1 < len(a.toks) && a.toks[j+1].Kind == Word && sparkDescribeSkip[a.toks[j+1].Text] {
			j++
		}
		if j+1 < len(a.toks) && a.toks[j+1].Kind == Word && sparkDescribeObject[a.toks[j+1].Text] {
			return i
		}
	default:
		return i
	}
	next, rel, ok := a.relationAt(j, j)
	if !ok {
		// `VACUUM '/path'` and `CACHE SELECT ...` name no relation, and
		// the tokens stay for the walk.
		return i
	}
	rel.Access = acc
	a.addRelation(rel)
	return next
}

// opaque reports why a head keyword's effect cannot be read, under the
// analyzer's dialect.
func (a *analyzer) opaque(word string) (string, bool) {
	if a.d == Oracle {
		if why, ok := oracleOpaque[word]; ok {
			return why, true
		}
	}
	why, ok := opaque[word]
	return why, ok
}

// storedUnit recognises Oracle's CREATE of a stored PL/SQL or Java unit at
// the CREATE in position i and, when it is one, returns the index of the
// last token, having recorded what the header names.
//
// PostgreSQL dollar-quotes a function body, so the scanner already reads it
// as a literal. Oracle writes the body inline: `CREATE PROCEDURE p AS BEGIN
// DELETE FROM t; END;` walked as SQL reports a delete of t that nothing
// performs, and the BEGIN inside marks the statement unreadable, so every
// migration defining a procedure would be refused as unknown. The body is
// source text Oracle compiles — verified by the server storing it with
// compilation errors rather than running any trailing statement — so the
// scan stops at the header.
//
// A trigger's header names the table it fires on, and that relation is
// recorded as a DDL target, as CREATE TRIGGER ... ON t does in PostgreSQL.
// `ON DATABASE` and `ON SCHEMA` name no relation; `ON NESTED TABLE c OF v`
// names v.
func (a *analyzer) storedUnit(i int) (int, bool) {
	j := i + 1
	for j < len(a.toks) && a.toks[j].Kind == Word && oracleCreateModifier[a.toks[j].Text] {
		j++
	}
	if j >= len(a.toks) || a.toks[j].Kind != Word || !oracleStoredUnit[a.toks[j].Text] {
		return i, false
	}
	last := len(a.toks) - 1
	if a.toks[j].Text != "trigger" {
		return last, true
	}
	if rel, ok := a.triggerTable(j); ok {
		a.addRelation(rel)
	}
	return last, true
}

// triggerTable finds the relation a trigger header fires on: the name after
// the first top-level ON, scanning from the TRIGGER keyword at j to the
// body. It reports false for a DATABASE or SCHEMA event trigger.
func (a *analyzer) triggerTable(j int) (Relation, bool) {
	depth := 0
	for k := j + 1; k < len(a.toks); k++ {
		t := a.toks[k]
		switch {
		case a.puncts(k, "("):
			depth++
		case a.puncts(k, ")"):
			depth--
		case t.Kind != Word || depth != 0:
		case triggerBody[t.Text]:
			return Relation{}, false
		case t.Text == "on":
			intro := k
			if k+1 < len(a.toks) && a.toks[k+1].isWord("nested") {
				// ON NESTED TABLE column OF view: the relation follows OF.
				intro = slices.IndexFunc(a.toks[k:], func(t Token) bool { return t.isWord("of") })
				if intro < 0 {
					return Relation{}, false
				}
				intro += k
			}
			if n := intro + 1; n < len(a.toks) && (a.toks[n].isWord("database") ||
				a.toks[n].isWord("schema") || a.toks[n].isWord("pluggable")) {
				return Relation{}, false
			}
			_, rel, ok := a.relationAt(intro, intro)
			// Access is decided from the introducer's text, and OF is
			// not a DDL introducer; either way the trigger acts on it.
			rel.Access = Write
			return rel, ok
		}
	}
	return Relation{}, false
}

// relationsAfter resolves the comma-separated relation list following an
// introducer at index i, and returns the index it consumed through.
//
// A list, not one name: `TRUNCATE TABLE a, b` and `DROP TABLE a, b` name
// several relations under one keyword, and stopping at the head means a rule
// guarding b never fires.
func (a *analyzer) relationsAfter(i int) (int, []Relation, bool) {
	if !introduces(a.toks[i].Text, a.top().verb) {
		return i, nil, false
	}
	var out []Relation
	j := i
	for {
		next, rel, ok := a.relationAt(i, j)
		if !ok {
			break
		}
		out = append(out, rel)
		j = next
		// Continue only across a comma at this nesting level. Anything
		// else ends the list.
		if j+1 < len(a.toks) && a.toks[j+1].Kind == Punct && a.toks[j+1].Text == "," {
			j++
			continue
		}
		break
	}
	return j, out, len(out) > 0
}

// relationAt resolves one name starting after position j, under the
// introducer at position i.
func (a *analyzer) relationAt(i, j int) (int, Relation, bool) {
	j++
	for j < len(a.toks) && a.toks[j].Kind == Word && relSkip[a.toks[j].Text] {
		j++
	}
	if j >= len(a.toks) || !a.toks[j].isName() {
		return j, Relation{}, false
	}
	// A bare clause keyword sits in a relation position without naming one:
	// `WHEN MATCHED THEN UPDATE SET x = 1` has an UPDATE with no target of
	// its own, and `COPY t FROM STDIN` ends in a direction, not a table.
	// Quoted identifiers never reach this test, so a table whose name is
	// "set" still works.
	if a.toks[j].Kind == Word && notARelation[a.toks[j].Text] {
		return j, Relation{}, false
	}
	// Two Spark positions look like relations and are not: INSERT
	// OVERWRITE [LOCAL] DIRECTORY writes files at a path, and the USING of
	// `CREATE TABLE t (...) USING DELTA` names a data source. Left alone,
	// every Databricks CREATE TABLE reads a relation called delta.
	if a.d == Spark && (a.toks[i].Text == "overwrite" && a.toks[j].isWord("directory") ||
		a.toks[i].Text == "using" && a.top().verb == Create) {
		return j, Relation{}, false
	}

	// A schema-qualified name arrives as three tokens. Quoted parts keep
	// their case; bare parts are already lowercased.
	name := a.toks[j].Text
	for j+2 < len(a.toks) && a.toks[j+1].Kind == Punct && a.toks[j+1].Text == "." &&
		a.toks[j+2].isName() {
		name += "." + a.toks[j+2].Text
		j += 2
	}

	// Oracle names a remote object `emp@link`, the link dotted and
	// optionally qualified: `emp@hq.example.com@hr`. The relation is the
	// OBJECT, reported under its own name: a rule guarding emp must fire
	// through a loopback link to the same database, and reporting
	// "emp@loop" would be a new name nothing was written against. The
	// link is consumed so a following comma still continues the list;
	// left in place, `FROM a@l, b` lost b.
	if a.d == Oracle && a.puncts(j+1, "@") && j+2 < len(a.toks) && a.toks[j+2].isName() {
		j += 2
		for (a.puncts(j+1, ".") || a.puncts(j+1, "@")) && j+2 < len(a.toks) && a.toks[j+2].isName() {
			j += 2
		}
	}

	// A CTE alias is not a relation. Reporting it would put someone's
	// `WITH doomed AS ...` into a table list beside real objects.
	if a.cteNames[name] {
		return j, Relation{}, false
	}
	r := a.top()
	if a.d == Oracle && a.oracleNoRelation(r, a.toks[i].Text) {
		return j, Relation{}, false
	}
	// A set-returning function in a FROM position is not a relation:
	// `FROM generate_series(1,10)`. The same lookahead must NOT run after
	// INTO or UPDATE, where a parenthesis opens a COLUMN LIST and the name
	// before it is the target: `INSERT INTO orders (id) VALUES (1)`.
	switch a.toks[i].Text {
	case "from", "join", "using":
		if j+1 < len(a.toks) && a.toks[j+1].Kind == Punct && a.toks[j+1].Text == "(" {
			return j, Relation{}, false
		}
	}

	acc := a.access(r, a.toks[i].Text)
	if r.verb == Copy && a.toks[i].Text == "copy" {
		// COPY's direction is a keyword AFTER the relation, so it is the
		// one access this scanner cannot decide from the introducer.
		acc = a.copyDirection(j)
	}
	return j, Relation{Name: name, Access: acc}, true
}

// puncts reports whether the token at k is the punctuation p.
func (a *analyzer) puncts(k int, p string) bool {
	return k < len(a.toks) && a.toks[k].Kind == Punct && a.toks[k].Text == p
}

// oracleNoRelation reports whether an Oracle introducer names something
// other than a relation here.
//
// Inside PL/SQL, INTO in SELECT ... INTO, FETCH ... INTO and UPDATE/DELETE
// ... RETURNING ... INTO assigns to variables; under the PostgreSQL rule,
// where SELECT INTO creates a table, `SELECT count(*) INTO n FROM t`
// reported a write of n. EXECUTE IMMEDIATE ... USING binds values the same
// way. INSERT and MERGE keep INTO as a target even there: a variable after
// INSERT ... RETURNING reads as a spurious write, which a lane survives,
// while tracking RETURNING — not reserved in Oracle, so `WHEN returning > 1
// THEN INTO b` is legal — could hide b. Outside PL/SQL a RETURNING target
// is a :bind, which never names a relation.
//
// UPDATE inside a SELECT is the row-lock clause, `FOR UPDATE OF col`, and
// OF is reserved in Oracle, so the name after it is a column. The select
// stays a select, as in PostgreSQL.
func (a *analyzer) oracleNoRelation(r *region, intro string) bool {
	switch intro {
	case "into":
		return a.plsql && r.verb != Insert && r.verb != Merge
	case "using":
		return r.verb == Call
	case "update":
		return r.verb == Select
	}
	return false
}

// copyDirection resolves COPY's access from the keyword following the
// relation. FROM loads and writes; TO exports and reads.
//
// An export misreported as a write escapes the rule that catches it:
// `COPY customers TO PROGRAM 'curl ...'` is a read, and a rule watching READS
// of customers is what fires on it. PostgreSQL models the same distinction as
// one bool, CopyStmt.is_from.
//
// An optional column list may sit between, so the scan steps over a balanced
// parenthesis group. A COPY with neither keyword is malformed; Write is the
// conservative reading.
func (a *analyzer) copyDirection(j int) Access {
	depth := 0
	for k := j + 1; k < len(a.toks); k++ {
		t := a.toks[k]
		if t.Kind == Punct {
			switch t.Text {
			case "(":
				depth++
			case ")":
				depth--
			case ";":
				return Write
			}
			continue
		}
		if depth != 0 || t.Kind != Word {
			continue
		}
		switch t.Text {
		case "to":
			return Read
		case "from":
			return Write
		}
	}
	return Write
}

// access decides read or write from the governing verb and the introducer.
//
// Position is what separates a target from a source: `DELETE FROM a USING b`
// and `UPDATE a SET x FROM b` both write the first relation and read the
// second, and the keyword alone cannot say which is which.
func (a *analyzer) access(r *region, intro string) Access {
	switch r.verb {
	case Select:
		// SELECT ... INTO t is CREATE TABLE t AS in another spelling.
		if intro == "into" {
			return Write
		}
	case Delete:
		if intro == "from" && r.firstTarget {
			r.firstTarget = false
			return Write
		}
	case Update:
		if intro == "update" && r.firstTarget {
			r.firstTarget = false
			return Write
		}
	case Insert, Merge:
		// Oracle's multi-table INSERT ALL / INSERT FIRST names a target
		// after EVERY `INTO`; only the first claimed a write, so
		// `INSERT ALL INTO a ... INTO b ... SELECT ...` hid the write of b
		// as a read. See oracleNoRelation for the INTO that names a variable.
		// Spark's INSERT OVERWRITE names its target after OVERWRITE, an
		// introducer only that dialect has.
		if (intro == "into" || intro == "overwrite") && (r.firstTarget || (a.d == Oracle && r.verb == Insert)) {
			r.firstTarget = false
			return Write
		}
	case Copy:
		// The relation named by COPY itself; copyDirection then decides
		// read or write from the FROM/TO that follows it. STDIN and
		// STDOUT sit after that keyword and notARelation drops them.
		//
		// Databricks' COPY INTO t loads files into t, and names it after
		// INTO. Read as the PostgreSQL form, t was a read, the copy wrote
		// nothing, and inspect reported a bulk load as a select.
		if intro == "copy" || a.d == Spark && intro == "into" {
			return Write
		}
	default:
		if !ddlVerb(r.verb) {
			break
		}
		// A DDL or privilege statement acts ON its target. CREATE INDEX
		// takes an exclusive lock and rewrites storage, GRANT rewrites
		// an ACL, REFRESH repopulates a matview: all of them change the
		// named object rather than reading rows from it.
		switch intro {
		case "table", "view", "into", "on", "truncate", "rename":
			return Write
		}
	}
	return Read
}

func (a *analyzer) addRelation(rel Relation) {
	for i := range a.rels {
		if a.rels[i].Name != rel.Name {
			continue
		}
		// Write dominates: a relation both written and read is written.
		if rel.Access == Write {
			a.rels[i].Access = Write
		}
		return
	}
	a.rels = append(a.rels, rel)
}

func (a *analyzer) prevWord(i int) (string, bool) {
	for j := i - 1; j >= 0; j-- {
		if a.toks[j].Kind == Word {
			return a.toks[j].Text, true
		}
		if a.toks[j].Kind == Punct && a.toks[j].Text != "," {
			return "", false
		}
	}
	return "", false
}

func (a *analyzer) result() Analysis {
	verb := Unknown
	if len(a.effects) > 0 {
		verb = a.effects[0]
	} else if a.explainSeen {
		verb = Explain
	} else if a.sawStatement {
		verb = Other
	}

	effects := a.effects
	if a.explainSeen && !a.analyzeSeen {
		// A plan is not an execution. Keeping the mutating effects here
		// would refuse `EXPLAIN DELETE ...`, which changes nothing and is
		// how a developer checks whether their WHERE clause is right.
		effects = nil
		verb = Explain
	}

	return Analysis{
		Verb:      verb,
		Effects:   effects,
		Relations: a.rels,
		Complete:  a.incomplete == "",
		Reason:    a.incomplete,
	}
}

// plsqlNotDML reports whether a DML word at i inside PL/SQL is not a
// statement: a member call (`v_tab.DELETE`, `r.update`) after a dot, or the
// row lock of `SELECT ... FOR UPDATE`. Neither runs DML, and reading one as
// a delete or update would let a rule naming it refuse a block that writes
// nothing.
func (a *analyzer) plsqlNotDML(i int) bool {
	if i == 0 {
		return false
	}
	return a.puncts(i-1, ".") || a.forUpdate(i)
}

// forUpdate reports whether the token at i is the UPDATE of a FOR UPDATE
// row lock.
func (a *analyzer) forUpdate(i int) bool {
	return i > 0 && a.toks[i].isWord("update") && a.toks[i-1].isWord("for")
}

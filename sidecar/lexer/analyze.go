package lexer

import "slices"

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

	incomplete string
}

// Analyze reports what one SQL statement does.
//
// It never returns an error. A statement it cannot follow comes back with
// Complete=false and a Reason, because a caller on a data path needs a
// verdict rather than an error to log.
func Analyze(sql string, d Dialect) Analysis {
	if d == MySQL {
		return analyzeMySQL(sql)
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

// analyzeWith runs one pass with an explicit rule set.
func analyzeWith(sql string, rules lexRules, d Dialect) Analysis {
	toks, bad := scanWith(sql, rules)
	a := &analyzer{
		toks:       toks,
		d:          d,
		stack:      []region{{kind: regTop, verb: Unknown}},
		cteNames:   map[string]bool{},
		incomplete: bad,
	}
	a.walk()
	return a.result()
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
				a.inCTEList, a.expectCTEName = false, false
				atHead = true
				continue
			case ",":
				if a.inCTEList && len(a.stack) == 1 {
					a.expectCTEName = true
				}
				atHead = a.wrapper
				continue
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
			a.inCTEList = true
			a.expectCTEName = true
			a.sawStatement = true
			atHead = false
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

		if atHead && a.head(t, i) {
			// Several keywords are both a verb and a relation
			// introducer: UPDATE t, TRUNCATE t, COPY t. Consuming the
			// head must not skip the target they name.
			if relIntro[t.Text] {
				if j, rels, ok := a.relationsAfter(i); ok {
					for _, rel := range rels {
						a.addRelation(rel)
					}
					i = j
				}
			}
			atHead = false
			continue
		}

		// A relation introducer claims the list that follows.
		if relIntro[t.Text] {
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
	return headAfter[word]
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
	verb, ok := statementVerb[t.Text]
	if !ok {
		return false
	}
	a.sawStatement = true

	if why, bad := opaque[t.Text]; bad {
		a.fail(why)
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

	// A schema-qualified name arrives as three tokens. Quoted parts keep
	// their case; bare parts are already lowercased.
	name := a.toks[j].Text
	for j+2 < len(a.toks) && a.toks[j+1].Kind == Punct && a.toks[j+1].Text == "." &&
		a.toks[j+2].isName() {
		name += "." + a.toks[j+2].Text
		j += 2
	}

	// A CTE alias is not a relation. Reporting it would put someone's
	// `WITH doomed AS ...` into a table list beside real objects.
	if a.cteNames[name] {
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

	r := a.top()
	acc := a.access(r, a.toks[i].Text)
	if r.verb == Copy && a.toks[i].Text == "copy" {
		// COPY's direction is a keyword AFTER the relation, so it is the
		// one access this scanner cannot decide from the introducer.
		acc = a.copyDirection(j)
	}
	return j, Relation{Name: name, Access: acc}, true
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
		if intro == "into" && r.firstTarget {
			r.firstTarget = false
			return Write
		}
	case Copy:
		// The relation named by COPY itself; copyDirection then decides
		// read or write from the FROM/TO that follows it. STDIN and
		// STDOUT sit after that keyword and notARelation drops them.
		if intro == "copy" {
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
		case "table", "view", "into", "on", "truncate":
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

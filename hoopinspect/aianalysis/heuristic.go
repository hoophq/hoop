package aianalysis

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hoophq/hoopinspect"
)

// Rule names a HeuristicAnalyzer finding. They are stable identifiers: they
// end up in audit metadata and in a deployment's "suppress this rule" config,
// so renaming one breaks someone's dashboard.
const (
	RuleUnboundedDelete   = "unbounded_delete"
	RuleUnboundedUpdate   = "unbounded_update"
	RuleDropObject        = "drop_object"
	RuleTruncate          = "truncate"
	RulePrivilegeChange   = "privilege_change"
	RuleSensitiveTable    = "sensitive_table_read"
	RuleGraphQLDestroy    = "graphql_destructive_mutation"
	RuleHTTPUnboundedDel  = "http_unbounded_delete"
	RuleCustomPattern     = "custom_pattern"
	RuleBulkRead          = "bulk_read"
	RuleSchemaRead        = "schema_read"
	RuleSchemaChange      = "schema_change"
	RuleGraphQLDepth      = "graphql_depth"
	RuleServerError       = "server_error"
	RuleRecognizedRoutine = "routine"
)

// DefaultGraphQLDepth is the nesting depth above which a query is flagged.
// Six is deeper than a hand-written product query and shallower than the
// recursive `posts { author { posts { ... } } }` amplification that turns one
// request into a full table scan.
const DefaultGraphQLDepth = 6

// destructiveGraphQLPrefixes are the root-field name shapes that mean a
// mutation destroys or escalates rather than edits. Matched as a prefix on the
// lowercased field name, because the convention in every GraphQL schema is
// verb-first: deleteUser, purgeAccount, resetPassword.
var destructiveGraphQLPrefixes = []string{
	"delete", "remove", "destroy", "purge", "drop", "truncate",
	"wipe", "reset", "revoke", "disable", "deactivate",
}

// HeuristicConfig configures HeuristicAnalyzer.
type HeuristicConfig struct {
	// SensitiveTables names relations whose contents are worth an alert even
	// on a plain SELECT. A bare name matches any schema qualification, so
	// "customers" matches "public.customers" — same rule as policy.MatchTable,
	// because an operator writing both configs must not have to remember two
	// matching semantics.
	SensitiveTables []string

	// MaxGraphQLDepth flags queries nested deeper than this. Zero uses
	// DefaultGraphQLDepth; negative disables the check.
	MaxGraphQLDepth int

	// Patterns are deployment-specific triggers evaluated after the built-in
	// rules, so a site can flag its own shapes ("nolock hint on the ledger")
	// without forking the analyzer.
	Patterns []Pattern
}

// Pattern is a caller-supplied regex trigger.
type Pattern struct {
	// Name identifies the finding. Required and must be unique.
	Name string

	// Regex is an RE2 expression matched against the statement text,
	// case-insensitively unless the expression sets its own flags.
	Regex string

	// RiskLevel is what a match scores. Required and must be a defined level.
	RiskLevel RiskLevel

	// Title and Explanation are shown to a human. Explanation is required:
	// an unexplained badge is a badge nobody trusts.
	Title       string
	Explanation string

	compiled *regexp.Regexp
}

// HeuristicAnalyzer scores statements with static rules and no model.
//
// It is safe for concurrent use: construction compiles everything and Analyze
// only reads.
type HeuristicAnalyzer struct {
	sensitive []string
	maxDepth  int
	patterns  []Pattern
}

var _ Analyzer = (*HeuristicAnalyzer)(nil)

// NewHeuristic validates and compiles a config.
//
// It reports EVERY problem in one error rather than the first, because a
// config with four bad regexes should take one round trip to fix, not four.
func NewHeuristic(cfg HeuristicConfig) (*HeuristicAnalyzer, error) {
	var problems []string

	sensitive := make([]string, 0, len(cfg.SensitiveTables))
	seenTable := make(map[string]bool, len(cfg.SensitiveTables))
	for i, t := range cfg.SensitiveTables {
		name := strings.ToLower(strings.TrimSpace(t))
		if name == "" {
			problems = append(problems, fmt.Sprintf("sensitive_tables[%d]: empty table name", i))
			continue
		}
		if seenTable[name] {
			continue
		}
		seenTable[name] = true
		sensitive = append(sensitive, name)
	}

	depth := cfg.MaxGraphQLDepth
	if depth == 0 {
		depth = DefaultGraphQLDepth
	}

	patterns := make([]Pattern, 0, len(cfg.Patterns))
	seenName := make(map[string]bool, len(cfg.Patterns))
	for i, p := range cfg.Patterns {
		label := p.Name
		if label == "" {
			label = fmt.Sprintf("patterns[%d]", i)
			problems = append(problems, label+": pattern has no name")
		} else if seenName[p.Name] {
			problems = append(problems, label+": duplicate pattern name")
		}
		seenName[p.Name] = true

		if p.Regex == "" {
			problems = append(problems, label+": pattern has no regex")
		} else {
			re, err := regexp.Compile("(?i)" + p.Regex)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: bad regex: %v", label, err))
			} else {
				p.compiled = re
			}
		}
		if !p.RiskLevel.Valid() {
			problems = append(problems, fmt.Sprintf("%s: risk_level %q is not low, medium or high", label, p.RiskLevel))
		}
		if strings.TrimSpace(p.Explanation) == "" {
			problems = append(problems, label+": pattern has no explanation")
		}
		if p.Title == "" {
			p.Title = p.Name
		}
		patterns = append(patterns, p)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("aianalysis: invalid heuristic config: %s", strings.Join(problems, "; "))
	}
	return &HeuristicAnalyzer{sensitive: sensitive, maxDepth: depth, patterns: patterns}, nil
}

// Analyze implements Analyzer. It never returns an error: every rule is a
// local string test, so there is nothing to fail. The signature keeps the
// error for LLM-backed implementations.
//
// A nil verdict means the statement matched no rule and carried no operation
// worth naming — "no opinion", not "safe".
func (h *HeuristicAnalyzer) Analyze(_ context.Context, stmt hoopinspect.Statement) (*Verdict, error) {
	if v := h.analyzeHigh(stmt); v != nil {
		return v, nil
	}
	if v := h.analyzeMedium(stmt); v != nil {
		return v, nil
	}
	// Custom patterns run last so a built-in high-risk finding is not masked
	// by a site rule that happens to also match.
	if v := h.matchPatterns(stmt); v != nil {
		return v, nil
	}
	return h.analyzeLow(stmt), nil
}

func (h *HeuristicAnalyzer) analyzeHigh(stmt hoopinspect.Statement) *Verdict {
	if stmt.HTTP != nil {
		// HTTP short-circuits the SQL rules below. The codec maps the DELETE
		// method onto OpDelete, the same constant a SQL DELETE carries, so
		// falling through would run "is there a WHERE clause" against an HTTP
		// request that has no SQL text and score every DELETE /users/42 as an
		// unbounded table wipe.
		return h.analyzeHTTPHigh(stmt.HTTP)
	}

	// Operation comes from the classifier, which already stripped comments and
	// string literals — so `SELECT '; DROP TABLE t'` is a select here, and a
	// verb hidden in a literal cannot promote a statement to high risk.
	switch stmt.Operation {
	case hoopinspect.OpDrop:
		return &Verdict{
			RiskLevel:   RiskHigh,
			Title:       "Object dropped",
			Rule:        RuleDropObject,
			Score:       0.95,
			Explanation: "DROP removes " + objectPhrase(stmt.Tables) + " and its data permanently; it cannot be rolled back on every engine.",
		}
	case hoopinspect.OpTruncate:
		return &Verdict{
			RiskLevel:   RiskHigh,
			Title:       "Table truncated",
			Rule:        RuleTruncate,
			Score:       0.9,
			Explanation: "TRUNCATE empties " + objectPhrase(stmt.Tables) + " in one statement, with no WHERE clause to limit it and usually no row-level undo.",
		}
	case hoopinspect.OpGrant, hoopinspect.OpRevoke:
		return &Verdict{
			RiskLevel:   RiskHigh,
			Title:       "Privileges changed",
			Rule:        RulePrivilegeChange,
			Score:       0.85,
			Explanation: strings.ToUpper(string(stmt.Operation)) + " changes who may access the database, which alters the blast radius of every later session.",
		}
	case hoopinspect.OpDelete:
		if !hasWhere(stmt.Text) {
			return &Verdict{
				RiskLevel:   RiskHigh,
				Title:       "Unbounded DELETE",
				Rule:        RuleUnboundedDelete,
				Score:       1.0,
				Explanation: "DELETE with no WHERE clause affects every row of " + objectPhrase(stmt.Tables) + ".",
			}
		}
	case hoopinspect.OpUpdate:
		if !hasWhere(stmt.Text) {
			return &Verdict{
				RiskLevel:   RiskHigh,
				Title:       "Unbounded UPDATE",
				Rule:        RuleUnboundedUpdate,
				Score:       0.98,
				Explanation: "UPDATE with no WHERE clause rewrites every row of " + objectPhrase(stmt.Tables) + ".",
			}
		}
	case hoopinspect.OpSelect:
		if t, ok := h.matchSensitive(stmt.Tables); ok {
			return &Verdict{
				RiskLevel:   RiskHigh,
				Title:       "Sensitive table read",
				Rule:        RuleSensitiveTable,
				Score:       0.8,
				Explanation: "This read touches " + t + ", which is configured as a sensitive table.",
			}
		}
	}
	return nil
}

// analyzeHTTPHigh scores an HTTP statement. d is never nil: the only caller
// reaches it after branching on stmt.HTTP.
func (h *HeuristicAnalyzer) analyzeHTTPHigh(d *hoopinspect.HTTPDetail) *Verdict {
	if gql := d.GraphQL; gql != nil && gql.OperationType == hoopinspect.OpMutation {
		if field, ok := destructiveRootField(gql.RootFields); ok {
			return &Verdict{
				RiskLevel: RiskHigh,
				Title:     "Destructive GraphQL mutation",
				Rule:      RuleGraphQLDestroy,
				Score:     0.9,
				Explanation: fmt.Sprintf("The mutation calls %q, a root field whose name says it destroys or revokes rather than edits.",
					field),
			}
		}
	}

	// A DELETE on a collection path (/users) removes the whole collection;
	// the same verb on /users/42 removes one row. Resource is the codec's
	// id-collapsed path, so /users/42 arrives as /users/* and is bounded.
	if d.Method == "DELETE" {
		if res, ok := unboundedResource(d); ok {
			return &Verdict{
				RiskLevel: RiskHigh,
				Title:     "Unbounded HTTP DELETE",
				Rule:      RuleHTTPUnboundedDel,
				Score:     0.92,
				Explanation: fmt.Sprintf("DELETE %s names a collection with no identifier or filter, so it targets every item under it.",
					res),
			}
		}
	}
	return nil
}

func (h *HeuristicAnalyzer) analyzeMedium(stmt hoopinspect.Statement) *Verdict {
	if d := stmt.HTTP; d != nil {
		if d.StatusCode >= 500 && d.StatusCode <= 599 {
			return &Verdict{
				RiskLevel: RiskMedium,
				Title:     "Upstream server error",
				Rule:      RuleServerError,
				Score:     0.5,
				Explanation: fmt.Sprintf("The upstream answered %d. A run of 5xx responses is how a failing dependency, or someone probing for one, shows up in an audit trail.",
					d.StatusCode),
			}
		}
		if gql := d.GraphQL; gql != nil && h.maxDepth > 0 && gql.Depth > h.maxDepth {
			return &Verdict{
				RiskLevel: RiskMedium,
				Title:     "Deeply nested GraphQL query",
				Rule:      RuleGraphQLDepth,
				Score:     0.55,
				Explanation: fmt.Sprintf("The selection set nests %d levels, above the configured limit of %d. Deep nesting is the standard GraphQL amplification vector: one request fans out into many resolver calls.",
					gql.Depth, h.maxDepth),
			}
		}
		// Same short-circuit as analyzeHigh: an HTTP statement has no SQL text
		// for the clause-shape rules below to read.
		return nil
	}

	switch stmt.Operation {
	case hoopinspect.OpAlter:
		return &Verdict{
			RiskLevel:   RiskMedium,
			Title:       "Schema altered",
			Rule:        RuleSchemaChange,
			Score:       0.6,
			Explanation: "ALTER changes the shape of " + objectPhrase(stmt.Tables) + ", which can lock the table and break clients that expect the old columns.",
		}
	case hoopinspect.OpShow:
		return &Verdict{
			RiskLevel:   RiskMedium,
			Title:       "Schema read",
			Rule:        RuleSchemaRead,
			Score:       0.35,
			Explanation: "This statement enumerates database structure rather than reading data, which is what reconnaissance looks like before a targeted query.",
		}
	case hoopinspect.OpSelect:
		if isSchemaCatalogRead(stmt.Tables) {
			return &Verdict{
				RiskLevel:   RiskMedium,
				Title:       "Schema read",
				Rule:        RuleSchemaRead,
				Score:       0.35,
				Explanation: "This SELECT reads the system catalog, enumerating tables and columns rather than application data.",
			}
		}
		if isBulkRead(stmt.Text) {
			return &Verdict{
				RiskLevel:   RiskMedium,
				Title:       "Bulk read",
				Rule:        RuleBulkRead,
				Score:       0.45,
				Explanation: "SELECT * with no WHERE or LIMIT returns every column of every row in " + objectPhrase(stmt.Tables) + ", which is how a full table leaves the database in one response.",
			}
		}
	}
	return nil
}

func (h *HeuristicAnalyzer) matchPatterns(stmt hoopinspect.Statement) *Verdict {
	for i := range h.patterns {
		p := &h.patterns[i]
		if p.compiled == nil || !p.compiled.MatchString(stmt.Text) {
			continue
		}
		return &Verdict{
			RiskLevel:   p.RiskLevel,
			Title:       p.Title,
			Rule:        RuleCustomPattern + ":" + p.Name,
			Score:       0.5,
			Explanation: p.Explanation,
		}
	}
	return nil
}

// analyzeLow reports a recognized-but-unremarkable statement. It returns nil
// for OpUnknown: a statement the classifier could not read has NOT been
// assessed, and calling that low risk would state a conclusion nobody reached.
func (h *HeuristicAnalyzer) analyzeLow(stmt hoopinspect.Statement) *Verdict {
	if stmt.Operation == hoopinspect.OpUnknown || stmt.Operation == "" {
		return nil
	}

	// The wording differs by protocol because "touching no sensitive table" is
	// a false claim about an HTTP request, which has no tables to touch. An
	// explanation a reader can tell is boilerplate is one they stop reading.
	what := "bounded in scope and touching no table flagged as sensitive"
	if stmt.HTTP != nil {
		what = "no destructive method on a collection, no server error, no oversized GraphQL document"
	}
	return &Verdict{
		RiskLevel: RiskLow,
		Title:     "Routine " + string(stmt.Operation),
		Rule:      RuleRecognizedRoutine,
		Score:     0.1,
		Explanation: fmt.Sprintf("A %s matching no risk rule: %s.",
			strings.ToUpper(string(stmt.Operation)), what),
	}
}

// matchSensitive reports the first configured sensitive table the statement
// touches, using policy.MatchTable's semantics: a bare configured name matches
// any schema qualification, and a qualified configured name matches exactly.
func (h *HeuristicAnalyzer) matchSensitive(tables []string) (string, bool) {
	for _, want := range h.sensitive {
		for _, got := range tables {
			got = strings.ToLower(got)
			if got == want || strings.HasSuffix(got, "."+want) {
				return got, true
			}
		}
	}
	return "", false
}

// catalogSchemas are the system schemas whose tables describe the database
// rather than hold application data.
var catalogSchemas = []string{"information_schema.", "pg_catalog.", "sys.", "mysql.", "sqlite_"}

func isSchemaCatalogRead(tables []string) bool {
	for _, t := range tables {
		t = strings.ToLower(t)
		for _, prefix := range catalogSchemas {
			if strings.HasPrefix(t, prefix) {
				return true
			}
		}
		// Unqualified catalog relations a client reaches via search_path.
		if strings.HasPrefix(t, "pg_") || t == "dual" {
			return true
		}
	}
	return false
}

// hasWhere reports whether the statement has a real WHERE clause.
//
// The evasion this defends against: `DELETE FROM audit_log -- WHERE keep=1`
// and `DELETE FROM t WHERE note='where'`-shaped text. A substring search for
// "where" says yes to the first, which would let an unbounded delete score as
// routine — the exact statement the analyzer exists to surface. So the text is
// run through the same comment- and literal-stripping the classifier uses and
// then tokenized, and only a standalone `where` token counts.
//
// A WHERE that only appears inside a subquery still counts as bounded here.
// That is a knowing false negative: a lexer cannot tell `DELETE FROM t WHERE
// id IN (SELECT ...)` from `DELETE FROM t; SELECT WHERE`, and over-reporting
// every DELETE as high risk is how a badge stops being read.
func hasWhere(sql string) bool {
	for _, tok := range tokenize(sql) {
		if tok == "where" {
			return true
		}
	}
	return false
}

// isBulkRead reports a `SELECT *` with neither a WHERE nor a LIMIT/TOP/FETCH
// bound. Same tokenization, same reason: a "limit" inside a comment does not
// bound anything.
func isBulkRead(sql string) bool {
	toks := tokenize(sql)
	star := false
	for i, tok := range toks {
		switch tok {
		case "where", "limit", "top", "fetch", "offset":
			return false
		case "*":
			// Only a `SELECT *` projection counts. `count(*)` returns one row
			// however many it scanned, and flagging it as a bulk read would
			// bury the real ones.
			if i > 0 && toks[i-1] == "select" {
				star = true
			}
		}
	}
	return star
}

// tokenize lowercases and splits SQL into keyword-candidate tokens. Comments,
// string literals and the contents of quoted identifiers are removed first, so
// text that merely LOOKS like a clause keyword cannot be mistaken for one.
//
// It does not reuse hoopinspect's internal stripSQLNoise, which is unexported
// and deliberately KEEPS quoted-identifier contents so `DELETE FROM "orders"`
// still yields a table name. That is right for extracting tables and wrong for
// finding clause keywords: here a relation named "where" must not become one.
func tokenize(sql string) []string {
	clean := stripNoise(sql)
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for i := range len(clean) {
		c := clean[i]
		switch {
		case c >= 'A' && c <= 'Z':
			cur.WriteByte(c + ('a' - 'A'))
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '$', c == '.':
			cur.WriteByte(c)
		case c == '*':
			flush()
			toks = append(toks, "*")
		default:
			flush()
		}
	}
	flush()
	return toks
}

// quotedIdent stands in for the contents of a quoted identifier. It cannot
// collide with a real keyword because a bare identifier token can never
// contain a space.
const quotedIdent = " ident "

// stripNoise removes line comments, block comments, single-quoted literals and
// the CONTENTS of quoted identifiers, replacing each with a space so token
// boundaries survive.
func stripNoise(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))

	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-',
			c == '#':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			b.WriteByte(' ')

		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 < len(sql) {
				i += 2
			} else {
				i = len(sql) // unterminated comment swallows the rest
			}
			b.WriteByte(' ')

		case c == '\'':
			i++
			for i < len(sql) {
				if sql[i] == '\'' {
					if i+1 < len(sql) && sql[i+1] == '\'' {
						i += 2 // '' is an escaped quote, not the end
						continue
					}
					i++
					break
				}
				i++
			}
			b.WriteByte(' ')

		// A quoted identifier is replaced by a placeholder rather than by its
		// contents. This tokenizer exists ONLY to find clause keywords, and a
		// table can legally be named "where" or `limit` — keeping the contents
		// would let `DELETE FROM "where"` read as a bounded delete. The
		// placeholder is still one identifier token, so it does not merge the
		// words on either side of it.
		case c == '"', c == '`':
			quote := c
			i++
			for i < len(sql) && sql[i] != quote {
				i++
			}
			if i < len(sql) {
				i++
			}
			b.WriteString(quotedIdent)

		case c == '[':
			i++
			for i < len(sql) && sql[i] != ']' {
				i++
			}
			if i < len(sql) {
				i++
			}
			b.WriteString(quotedIdent)

		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func destructiveRootField(fields []string) (string, bool) {
	for _, f := range fields {
		lower := strings.ToLower(f)
		for _, prefix := range destructiveGraphQLPrefixes {
			if strings.HasPrefix(lower, prefix) {
				return f, true
			}
		}
	}
	return "", false
}

// isUnboundedResource reports an HTTP path that names a collection rather than
// one item. A path whose codec-normalized form ends in "*" carries an id; a
// query string is treated as a filter, which bounds the delete enough that it
// is not the "wipe everything" shape.
func unboundedResource(d *hoopinspect.HTTPDetail) (string, bool) {
	if len(d.Query) > 0 {
		return "", false
	}
	res := d.Resource
	if res == "" {
		res = d.Path
	}
	// A bare "/" trims to empty: there is no collection to name, and claiming
	// one would put a path in the explanation that the request never had.
	trimmed := strings.TrimSuffix(res, "/")
	if trimmed == "" {
		return "", false
	}
	last := trimmed[strings.LastIndexByte(trimmed, '/')+1:]
	if last == "*" {
		return "", false
	}
	// A literal id the codec did not collapse still bounds the request. Any
	// digit in the last segment reads as an identifier, since /users/alice is
	// one user and /users is all of them.
	for i := range len(last) {
		if last[i] >= '0' && last[i] <= '9' {
			return "", false
		}
	}
	return res, true
}

// objectPhrase names the affected relations, falling back to language that
// admits ignorance. Tables is best-effort (see hoopinspect.ClassifySQL), and
// an explanation that asserts "every row of orders" when the parser never
// found `orders` is worse than one that says it could not tell.
func objectPhrase(tables []string) string {
	switch len(tables) {
	case 0:
		return "the target table (which could not be determined from the statement text)"
	case 1:
		return tables[0]
	default:
		return strings.Join(tables, ", ")
	}
}

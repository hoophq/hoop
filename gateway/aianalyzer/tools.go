package aianalyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	laia "github.com/hoophq/hoop/common/aianalyzer"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
)

const (
	metadataExecTimeout      = 30 * time.Second
	metadataOutputMaxChars   = 4000
	pastSessionsDefaultLimit = 10
	pastSessionsMaxLimit     = 25
)

// identifierRe validates table/schema identifiers passed to metadata queries.
// Only alphanumerics, underscore and dot are allowed (no quoting/injection surface).
var identifierRe = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

// Database-selection directives the webapp/CLI prepend to session scripts when
// the user picks a database: psql "\c <db>", mysql/mongodb "use <db>;", and
// mssql "USE [<db>];".
var (
	psqlConnectRe = regexp.MustCompile(`(?m)^\s*\\c(?:onnect)?\s+([A-Za-z0-9_$-]+)\s*$`)
	useDatabaseRe = regexp.MustCompile(`(?mi)^\s*use\s+[\[` + "`" + `]?([A-Za-z0-9_$-]+)[\]` + "`" + `]?\s*;`)
)

// SessionDatabaseFromScript extracts the database the session script targets,
// when the script carries an explicit selection directive (the webapp prepends
// "\c <db>" for postgres, "use <db>;" for mysql/mongodb and "USE [<db>];" for
// mssql when a database is picked). Returns "" when the script has no such
// directive, meaning the connection's default database applies.
func SessionDatabaseFromScript(subtype, script string) string {
	var m []string
	switch strings.ToLower(subtype) {
	case "postgres":
		m = psqlConnectRe.FindStringSubmatch(script)
	case "mysql", "mssql", "mongodb":
		m = useDatabaseRe.FindStringSubmatch(script)
	}
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// AnalyzerExecIdentity carries the credentials used to run metadata queries as
// auditable plain-exec sessions on behalf of the requesting user. Exactly one of
// BearerToken or ImpersonateUserSubject is set depending on the ingress path.
type AnalyzerExecIdentity struct {
	BearerToken            string
	ImpersonateUserSubject string
	UserAgent              string
}

// gatewayToolExecutor implements laia.ToolExecutor with the two investigation
// tools: search_past_sessions and run_metadata_query.
type gatewayToolExecutor struct {
	orgID          string
	conn           *models.Connection
	userID         string
	isAdminAuditor bool
	exec           AnalyzerExecIdentity
	// database is the database the analyzed session targets when its script
	// carries an explicit selection directive; metadata queries follow it.
	database string
}

// NewToolExecutor builds the gateway-side investigation tool executor.
// database, when non-empty, scopes metadata queries to the same database the
// analyzed session targets (see SessionDatabaseFromScript).
func NewToolExecutor(orgID string, conn *models.Connection, userID string, isAdminAuditor bool, exec AnalyzerExecIdentity, database string) laia.ToolExecutor {
	return &gatewayToolExecutor{
		orgID:          orgID,
		conn:           conn,
		userID:         userID,
		isAdminAuditor: isAdminAuditor,
		exec:           exec,
		database:       database,
	}
}

// InvestigationTools returns the investigation tool schemas offered to the model
// during the agentic loop.
func InvestigationTools() []laia.Tool {
	return []laia.Tool{
		{
			Name: "search_past_sessions",
			Description: "Review the current user's recent sessions to judge whether the behavior under analysis is routine or anomalous. " +
				"scope=current_connection restricts to this connection; scope=same_type includes sibling connections of the same resource type.",
			InputSchema: laia.ToolInputSchema{
				Properties: map[string]laia.ToolProperty{
					"scope": {
						Type:        "string",
						Description: "Which sessions to include.",
						Enum:        []string{"current_connection", "same_type"},
					},
					"limit": {
						Type:        "integer",
						Description: "Max sessions to return (default 10, max 25).",
					},
				},
				Required: []string{"scope"},
			},
		},
		{
			Name: "run_metadata_query",
			Description: "Run a read-only metadata query against the target database resource to estimate query cost/impact before classifying. " +
				"Supported for postgres, mysql, mssql, mongodb (collections; explain unavailable) and oracledb connections.",
			InputSchema: laia.ToolInputSchema{
				Properties: map[string]laia.ToolProperty{
					"operation": {
						Type:        "string",
						Description: "explain = query/execution plan (never executes the statement); table_size = row count and size; table_indexes = index usage. For mongodb, table = collection.",
						Enum:        []string{"explain", "table_size", "table_indexes"},
					},
					"query": {
						Type:        "string",
						Description: "The SQL statement to EXPLAIN. Required for operation=explain.",
					},
					"table": {
						Type:        "string",
						Description: "Table (or mongodb collection) name. Required for operation=table_size and table_indexes.",
					},
					"schema": {
						Type:        "string",
						Description: "Schema name. Optional; when omitted, postgres searches all user schemas and mysql all databases.",
					},
				},
				Required: []string{"operation"},
			},
		},
		{
			Name: "get_connection_context",
			Description: "Fetch governance context for the target connection: resource type/subtype, environment tags, reviewer groups, data masking, guardrails, and access modes. " +
				"Works for any connection type. Use it to judge resource sensitivity (e.g. production vs demo) when classifying.",
			InputSchema: laia.ToolInputSchema{Properties: map[string]laia.ToolProperty{}},
		},
	}
}

// Execute dispatches a tool call to its handler.
func (e *gatewayToolExecutor) Execute(ctx context.Context, name, arguments string) (string, bool) {
	switch name {
	case "search_past_sessions":
		return e.searchPastSessions(arguments)
	case "run_metadata_query":
		return e.runMetadataQuery(ctx, arguments)
	case "get_connection_context":
		return e.connectionContext()
	default:
		return fmt.Sprintf("unknown tool %q", name), true
	}
}

// likeEscaper neutralizes the LIKE wildcards in a value used as an exact filter.
// models.ListSessions applies opt.User with LIKE, so a subject containing '_'
// (valid in OIDC sub claims) would otherwise match other users' sessions.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

type searchPastSessionsArgs struct {
	Scope string `json:"scope"`
	Limit int    `json:"limit"`
}

type pastSessionRow struct {
	ID         string `json:"id"`
	Connection string `json:"connection"`
	Verb       string `json:"verb"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	CreatedAt  string `json:"created_at"`
}

func (e *gatewayToolExecutor) searchPastSessions(arguments string) (string, bool) {
	var args searchPastSessionsArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), true
	}
	limit := args.Limit
	if limit <= 0 {
		limit = pastSessionsDefaultLimit
	}
	if limit > pastSessionsMaxLimit {
		limit = pastSessionsMaxLimit
	}

	var connNames []string
	switch args.Scope {
	case "same_type":
		names, err := models.ListConnectionNamesByType(models.DB, e.orgID, e.conn.Type, e.conn.SubType.String)
		if err != nil {
			return fmt.Sprintf("failed listing connections of same type: %v", err), true
		}
		connNames = names
	case "current_connection", "":
		connNames = []string{e.conn.Name}
	default:
		return fmt.Sprintf("invalid scope %q; expected current_connection or same_type", args.Scope), true
	}
	if len(connNames) == 0 {
		connNames = []string{e.conn.Name}
	}

	perConn := limit / len(connNames)
	if perConn < 1 {
		perConn = 1
	}

	var rows []pastSessionRow
	var failures int
	for _, name := range connNames {
		opt := models.NewSessionOption()
		// Only Items is read below, and this loop runs once per connection in
		// scope: the default exact count would run an unbounded COUNT over
		// sessions for each of them and then discard every result.
		opt.CountMode = models.SessionCountNone
		opt.User = likeEscaper.Replace(e.userID) // exact match on the current user
		opt.ConnectionName = name
		opt.Limit = perConn
		list, err := models.ListSessions(e.orgID, e.userID, e.isAdminAuditor, opt)
		if err != nil {
			failures++
			log.Warnf("aianalyzer: failed listing past sessions for connection %q: %v", name, err)
			continue
		}
		for _, s := range list.Items {
			rows = append(rows, pastSessionRow{
				ID:         s.ID,
				Connection: s.Connection,
				Verb:       s.Verb,
				Status:     s.Status,
				ExitCode:   s.ExitCode,
				CreatedAt:  s.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
	}
	// Never present a total lookup failure as "this user has no history": that
	// reads to the classifier as evidence of normality rather than missing data.
	if len(rows) == 0 && failures == len(connNames) {
		return "failed listing past sessions for every connection in scope", true
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt > rows[j].CreatedAt })
	if len(rows) > limit {
		rows = rows[:limit]
	}

	out, err := json.Marshal(rows)
	if err != nil {
		return fmt.Sprintf("failed encoding result: %v", err), true
	}
	if len(rows) == 0 {
		return "[] (no matching past sessions for this user)", false
	}
	return string(out), false
}

type runMetadataQueryArgs struct {
	Operation string `json:"operation"`
	Query     string `json:"query"`
	Table     string `json:"table"`
	Schema    string `json:"schema"`
}

func (e *gatewayToolExecutor) runMetadataQuery(ctx context.Context, arguments string) (string, bool) {
	var args runMetadataQueryArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), true
	}

	subtype := strings.ToLower(e.conn.SubType.String)
	if e.conn.Type != "database" || !supportedMetadataSubtype(subtype) {
		return "metadata queries are only supported for postgres, mysql, mssql, mongodb and oracledb connections", true
	}

	// Argument validation lives in buildMetadataScript, beside the interpolation.
	script, buildErr := buildMetadataScript(subtype, args)
	if buildErr != "" {
		return buildErr, true
	}
	script = prependDatabaseDirective(subtype, e.database, script)

	output, execErr := e.runExec(ctx, script)
	if execErr != nil {
		return fmt.Sprintf("metadata query failed: %v", execErr), true
	}
	if msg := emptyResultMessage(subtype, args, output); msg != "" {
		return msg, true
	}
	if len(output) > metadataOutputMaxChars {
		output = output[:metadataOutputMaxChars] + "\n... (truncated)"
	}
	return output, false
}

// goBatchRe matches a sqlcmd batch separator on its own line.
var goBatchRe = regexp.MustCompile(`(?mi)^\s*GO\s*;?\s*$`)

// validateSingleStatement rejects an explain query that carries more than one
// statement. This is what makes "explain" read-only: the builders only wrap the
// FIRST statement (EXPLAIN … / SHOWPLAN batch / EXPLAIN PLAN FOR …), and every
// client splits the script on the statement separator, so a chained
// "SELECT 1; DELETE FROM t" would plan the SELECT and then EXECUTE the DELETE.
// The query is model-authored and the model reads the user's script, so it must
// be treated as untrusted.
//
// A single trailing semicolon is allowed. A semicolon inside a string literal
// is rejected too — a false positive the model recovers from by rewriting the
// query, which is the safe direction to err.
func validateSingleStatement(subtype, query string) string {
	trimmed := strings.TrimSpace(query)
	if body := strings.TrimSuffix(trimmed, ";"); strings.Contains(body, ";") {
		return "operation=explain accepts a single statement; remove the extra ';' and explain one statement at a time"
	}
	if subtype == "mssql" && goBatchRe.MatchString(trimmed) {
		return "operation=explain accepts a single statement; remove the GO batch separator"
	}
	return ""
}

// dbNameRe matches the same charset the extraction regexes accept.
var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_$-]+$`)

// prependDatabaseDirective scopes a metadata script to the database the analyzed
// session targets, mirroring the directive the webapp prepends to the session
// script ("\c <db>" for psql, "use <db>;" for mysql, "USE [<db>];" for mssql).
// No-op when the session uses the connection's default database or when the
// dialect has no database directive (oracledb).
func prependDatabaseDirective(subtype, database, script string) string {
	if database == "" || !dbNameRe.MatchString(database) {
		return script
	}
	switch subtype {
	case "postgres":
		return "\\set QUIET on\n\\c " + database + "\n\\set QUIET off\n" + script
	case "mysql":
		return "use " + database + ";\n" + script
	case "mssql":
		return "USE [" + database + "];\n" + script
	case "mongodb":
		return "db = db.getSiblingDB('" + database + "');\n" + script
	default:
		return script
	}
}

func supportedMetadataSubtype(subtype string) bool {
	switch subtype {
	case "postgres", "mysql", "mssql", "mongodb", "oracledb":
		return true
	}
	return false
}

// buildMetadataScript returns the dialect script for the requested operation, or
// a non-empty error string when the arguments are invalid.
//
// Validation lives here, next to the interpolation it protects, so no caller can
// reach a script builder with an unchecked identifier. Every script is read-only
// by construction: plans are produced without executing the statement (which is
// why explain is restricted to a single statement) and size/index lookups only
// read catalog/stat views.
func buildMetadataScript(subtype string, args runMetadataQueryArgs) (script string, errMsg string) {
	switch args.Operation {
	case "explain", "table_size", "table_indexes":
	default:
		return "", fmt.Sprintf("invalid operation %q; expected explain, table_size or table_indexes", args.Operation)
	}
	if args.Table != "" && !identifierRe.MatchString(args.Table) {
		return "", fmt.Sprintf("invalid table identifier %q", args.Table)
	}
	if args.Schema != "" && !identifierRe.MatchString(args.Schema) {
		return "", fmt.Sprintf("invalid schema identifier %q", args.Schema)
	}
	if args.Operation == "explain" {
		if strings.TrimSpace(args.Query) == "" {
			return "", "operation=explain requires a non-empty query"
		}
		if msg := validateSingleStatement(subtype, args.Query); msg != "" {
			return "", msg
		}
	}
	if args.Operation != "explain" && args.Table == "" {
		return "", fmt.Sprintf("operation=%s requires a table", args.Operation)
	}
	switch subtype {
	case "postgres":
		return pgMetadataScript(args), ""
	case "mysql":
		return mysqlMetadataScript(args), ""
	case "mssql":
		return mssqlMetadataScript(args), ""
	case "mongodb":
		if args.Operation == "explain" {
			return "", "explain is not supported for mongodb connections; use table_size or table_indexes on the referenced collections"
		}
		return mongoMetadataScript(args), ""
	case "oracledb":
		return oracleMetadataScript(args), ""
	default:
		return "", fmt.Sprintf("unsupported subtype %q", subtype)
	}
}

func pgMetadataScript(args runMetadataQueryArgs) string {
	switch args.Operation {
	case "explain":
		// Plain EXPLAIN never executes the statement (no ANALYZE).
		return "EXPLAIN " + args.Query
	case "table_size":
		schemaFilter := `n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg_%'`
		if args.Schema != "" {
			schemaFilter = fmt.Sprintf("n.nspname = '%s'", args.Schema)
		}
		return fmt.Sprintf(`SELECT n.nspname AS schema, c.relname, COALESCE(s.n_live_tup, 0) AS n_live_tup, pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_stat_all_tables s ON s.relid = c.oid
WHERE c.relname = '%s' AND c.relkind IN ('r', 'p', 'm') AND %s;`, args.Table, schemaFilter)
	default: // table_indexes
		schemaFilter := ""
		if args.Schema != "" {
			schemaFilter = fmt.Sprintf(" AND schemaname = '%s'", args.Schema)
		}
		return fmt.Sprintf(`SELECT schemaname, indexrelname, idx_scan, idx_tup_read FROM pg_stat_user_indexes WHERE relname = '%s'%s;
SELECT schemaname, indexname, indexdef FROM pg_indexes WHERE tablename = '%s'%s;`, args.Table, schemaFilter, args.Table, schemaFilter)
	}
}

func mysqlMetadataScript(args runMetadataQueryArgs) string {
	schemaFilter := ""
	if args.Schema != "" {
		schemaFilter = fmt.Sprintf(" AND table_schema = '%s'", args.Schema)
	}
	switch args.Operation {
	case "explain":
		return "EXPLAIN FORMAT=JSON " + args.Query
	case "table_size":
		return fmt.Sprintf(`SELECT table_schema, table_name, table_rows, ROUND(data_length/1024/1024, 2) AS data_mb, ROUND(index_length/1024/1024, 2) AS index_mb
FROM information_schema.tables WHERE table_name = '%s'%s;`, args.Table, schemaFilter)
	default: // table_indexes
		return fmt.Sprintf(`SELECT table_schema, index_name, column_name, cardinality FROM information_schema.statistics WHERE table_name = '%s'%s;`, args.Table, schemaFilter)
	}
}

// mssqlMetadataScript builds sqlcmd batches. SHOWPLAN_ALL must be alone in its
// batch, hence the GO separators; it returns the estimated plan without
// executing the statement.
func mssqlMetadataScript(args runMetadataQueryArgs) string {
	object := args.Table
	if args.Schema != "" {
		object = args.Schema + "." + args.Table
	}
	switch args.Operation {
	case "explain":
		return "SET SHOWPLAN_ALL ON\nGO\n" + args.Query + "\nGO\nSET SHOWPLAN_ALL OFF\nGO"
	case "table_size":
		return fmt.Sprintf(`SET NOCOUNT ON;
IF OBJECT_ID(N'%s') IS NULL
  PRINT 'table not found in the connection database'
ELSE
  EXEC sp_spaceused N'%s';`, object, object)
	default: // table_indexes
		return fmt.Sprintf(`SET NOCOUNT ON;
IF OBJECT_ID(N'%s') IS NULL
  PRINT 'table not found in the connection database'
ELSE
  SELECT i.name AS index_name, i.type_desc, i.is_unique, c.name AS column_name
  FROM sys.indexes i
  JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
  JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
  WHERE i.object_id = OBJECT_ID(N'%s');`, object, object)
	}
}

// mongoMetadataScript builds mongo shell JS; table names a collection.
func mongoMetadataScript(args runMetadataQueryArgs) string {
	if args.Operation == "table_size" {
		return fmt.Sprintf(`var s = db.getCollection('%s').stats();
print(JSON.stringify({ns: s.ns, count: s.count, avgObjSize: s.avgObjSize, size: s.size, storageSize: s.storageSize, totalIndexSize: s.totalIndexSize}));`, args.Table)
	}
	// table_indexes
	return fmt.Sprintf(`print(JSON.stringify(db.getCollection('%s').getIndexes()));`, args.Table)
}

func oracleMetadataScript(args runMetadataQueryArgs) string {
	switch args.Operation {
	case "explain":
		return "EXPLAIN PLAN FOR " + strings.TrimSuffix(strings.TrimSpace(args.Query), ";") + ";\nSELECT plan_table_output FROM table(dbms_xplan.display());"
	case "table_size":
		ownerFilter := "user_segments"
		if args.Schema != "" {
			return fmt.Sprintf(`SELECT owner, segment_name, segment_type, ROUND(bytes/1024/1024, 2) AS size_mb FROM dba_segments WHERE segment_name = UPPER('%s') AND owner = UPPER('%s');`, args.Table, args.Schema)
		}
		return fmt.Sprintf(`SELECT segment_name, segment_type, ROUND(bytes/1024/1024, 2) AS size_mb FROM %s WHERE segment_name = UPPER('%s');`, ownerFilter, args.Table)
	default: // table_indexes
		ownerFilter := ""
		if args.Schema != "" {
			ownerFilter = fmt.Sprintf(" AND owner = UPPER('%s')", args.Schema)
		}
		return fmt.Sprintf(`SELECT index_name, index_type, uniqueness, status FROM all_indexes WHERE table_name = UPPER('%s')%s;`, args.Table, ownerFilter)
	}
}

// emptyResultMessage converts an empty table_size/table_indexes result into an
// explicit tool error so the model (and reviewers reading the trace) see why the
// lookup found nothing instead of a bare "(0 rows)" header. Metadata queries run
// against the single database this connection targets; tables in other databases
// on the same server are intentionally out of scope.
func emptyResultMessage(subtype string, args runMetadataQueryArgs, output string) string {
	if args.Operation != "table_size" && args.Operation != "table_indexes" {
		return ""
	}
	var empty bool
	switch subtype {
	case "postgres":
		// psql prints a "(0 rows)" footer per statement; table_indexes runs two.
		stmts := 1
		if args.Operation == "table_indexes" {
			stmts = 2
		}
		empty = strings.Count(output, "(0 rows)") >= stmts
	case "mysql":
		// mysql CLI prints nothing when no rows match.
		empty = strings.TrimSpace(output) == ""
	case "oracledb":
		empty = strings.Contains(strings.ToLower(output), "no rows selected")
	default:
		// mssql scripts print an explicit not-found message themselves and
		// mongodb errors on missing collections; pass the output through.
		return ""
	}
	if !empty {
		return ""
	}
	scope := "any schema of the connection's database"
	if args.Schema != "" {
		scope = fmt.Sprintf("schema %q of the connection's database", args.Schema)
	}
	if args.Operation == "table_indexes" {
		return fmt.Sprintf("no indexes found for table %q in %s (the table may not exist; this tool only sees the database this connection targets)", args.Table, scope)
	}
	return fmt.Sprintf("table %q not found in %s (this tool only sees the database this connection targets)", args.Table, scope)
}

// connectionContext returns governance metadata of the target connection so the
// model can factor resource sensitivity into the verdict. Works for every
// connection type; read from the already-loaded connection row (no exec).
func (e *gatewayToolExecutor) connectionContext() (string, bool) {
	ctx := map[string]any{
		"name":                 e.conn.Name,
		"type":                 e.conn.Type,
		"subtype":              e.conn.SubType.String,
		"tags":                 e.conn.ConnectionTags,
		"legacy_tags":          []string(e.conn.Tags),
		"reviewer_groups":      []string(e.conn.Reviewers),
		"redact_enabled":       e.conn.RedactEnabled,
		"redact_types":         []string(e.conn.RedactTypes),
		"guardrail_rule_count": len(e.conn.GuardRailRules),
		"access_mode": map[string]string{
			"exec":     e.conn.AccessModeExec,
			"connect":  e.conn.AccessModeConnect,
			"runbooks": e.conn.AccessModeRunbooks,
		},
		"agent_mode": e.conn.AgentMode,
	}
	out, err := json.Marshal(ctx)
	if err != nil {
		return fmt.Sprintf("failed encoding connection context: %v", err), true
	}
	return string(out), false
}

// runExec runs a read-only metadata script as an auditable plain-exec session,
// mirroring the schema explorer pattern.
func (e *gatewayToolExecutor) runExec(ctx context.Context, script string) (string, error) {
	sessionID := uuid.NewString()
	// Metadata execs are always suffixed so they stay filterable in the audit
	// trail regardless of which ingress path set the base user-agent.
	userAgent := "aianalyzer.metadata"
	if e.exec.UserAgent != "" {
		userAgent = e.exec.UserAgent + ".metadata"
	}
	client, err := clientexec.New(&clientexec.Options{
		OrgID:                  e.orgID,
		SessionID:              sessionID,
		ConnectionName:         e.conn.Name,
		BearerToken:            e.exec.BearerToken,
		ImpersonateUserSubject: e.exec.ImpersonateUserSubject,
		UserAgent:              userAgent,
		Verb:                   pb.ClientVerbPlainExec,
	})
	if err != nil {
		return "", fmt.Errorf("failed creating exec client: %w", err)
	}
	respCh := make(chan *clientexec.Response, 1)
	go func() {
		defer func() { close(respCh); client.Close() }()
		respCh <- client.Run([]byte(script), nil)
	}()
	// Bound by BOTH the per-exec timeout and the caller's remaining budget, so a
	// tool call cannot run past the agentic loop's deadline.
	timeoutCtx, cancelFn := context.WithTimeout(ctx, metadataExecTimeout)
	defer cancelFn()
	select {
	case resp := <-respCh:
		// clientexec reports transport/system failures as ExitCode -2 with the
		// error text in Output; only OutputStatus distinguishes those from a
		// successful run whose exit code could not be parsed.
		if resp.OutputStatus == "failed" || (resp.ExitCode != 0 && resp.ExitCode != -2) {
			return "", fmt.Errorf("exit_code=%d output=%s", resp.ExitCode, resp.Output)
		}
		return resp.Output, nil
	case <-timeoutCtx.Done():
		client.Close()
		return "", fmt.Errorf("metadata query aborted: %w", timeoutCtx.Err())
	}
}

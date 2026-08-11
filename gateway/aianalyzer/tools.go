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
}

// NewToolExecutor builds the gateway-side investigation tool executor.
func NewToolExecutor(orgID string, conn *models.Connection, userID string, isAdminAuditor bool, exec AnalyzerExecIdentity) laia.ToolExecutor {
	return &gatewayToolExecutor{
		orgID:          orgID,
		conn:           conn,
		userID:         userID,
		isAdminAuditor: isAdminAuditor,
		exec:           exec,
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
				"Supported only for postgres and mysql connections.",
			InputSchema: laia.ToolInputSchema{
				Properties: map[string]laia.ToolProperty{
					"operation": {
						Type:        "string",
						Description: "explain = query plan (does not execute the query); table_size = row count and size; table_indexes = index usage.",
						Enum:        []string{"explain", "table_size", "table_indexes"},
					},
					"query": {
						Type:        "string",
						Description: "The SQL statement to EXPLAIN. Required for operation=explain.",
					},
					"table": {
						Type:        "string",
						Description: "Table name. Required for operation=table_size and table_indexes.",
					},
					"schema": {
						Type:        "string",
						Description: "Schema name. Optional; defaults to public (postgres) or the connection default.",
					},
				},
				Required: []string{"operation"},
			},
		},
	}
}

// Execute dispatches a tool call to its handler.
func (e *gatewayToolExecutor) Execute(ctx context.Context, name, arguments string) (string, bool) {
	switch name {
	case "search_past_sessions":
		return e.searchPastSessions(arguments)
	case "run_metadata_query":
		return e.runMetadataQuery(arguments)
	default:
		return fmt.Sprintf("unknown tool %q", name), true
	}
}

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
	for _, name := range connNames {
		opt := models.NewSessionOption()
		opt.User = e.userID // exact match on the current user
		opt.ConnectionName = name
		opt.Limit = perConn
		list, err := models.ListSessions(e.orgID, e.userID, e.isAdminAuditor, opt)
		if err != nil {
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

func (e *gatewayToolExecutor) runMetadataQuery(arguments string) (string, bool) {
	var args runMetadataQueryArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), true
	}

	subtype := strings.ToLower(e.conn.SubType.String)
	if e.conn.Type != "database" || (subtype != "postgres" && subtype != "mysql") {
		return "metadata queries are only supported for postgres and mysql connections", true
	}

	if args.Table != "" && !identifierRe.MatchString(args.Table) {
		return fmt.Sprintf("invalid table identifier %q", args.Table), true
	}
	if args.Schema != "" && !identifierRe.MatchString(args.Schema) {
		return fmt.Sprintf("invalid schema identifier %q", args.Schema), true
	}

	script, buildErr := buildMetadataScript(subtype, args)
	if buildErr != "" {
		return buildErr, true
	}

	output, execErr := e.runExec(script)
	if execErr != nil {
		return fmt.Sprintf("metadata query failed: %v", execErr), true
	}
	if len(output) > metadataOutputMaxChars {
		output = output[:metadataOutputMaxChars] + "\n... (truncated)"
	}
	return output, false
}

// buildMetadataScript returns the dialect SQL for the requested operation, or a
// non-empty error string when the arguments are invalid.
func buildMetadataScript(subtype string, args runMetadataQueryArgs) (script string, errMsg string) {
	switch args.Operation {
	case "explain":
		if strings.TrimSpace(args.Query) == "" {
			return "", "operation=explain requires a non-empty query"
		}
		if subtype == "postgres" {
			// Plain EXPLAIN never executes the statement (no ANALYZE).
			return "EXPLAIN " + args.Query, ""
		}
		return "EXPLAIN FORMAT=JSON " + args.Query, ""
	case "table_size":
		if args.Table == "" {
			return "", "operation=table_size requires a table"
		}
		if subtype == "postgres" {
			schema := args.Schema
			if schema == "" {
				schema = "public"
			}
			return fmt.Sprintf(`SELECT c.relname, COALESCE(s.n_live_tup, 0) AS n_live_tup, pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_stat_all_tables s ON s.relid = c.oid
WHERE c.relname = '%s' AND n.nspname = '%s';`, args.Table, schema), ""
		}
		return fmt.Sprintf(`SELECT table_name, table_rows, ROUND(data_length/1024/1024, 2) AS data_mb, ROUND(index_length/1024/1024, 2) AS index_mb
FROM information_schema.tables WHERE table_name = '%s';`, args.Table), ""
	case "table_indexes":
		if args.Table == "" {
			return "", "operation=table_indexes requires a table"
		}
		if subtype == "postgres" {
			return fmt.Sprintf(`SELECT indexrelname, idx_scan, idx_tup_read FROM pg_stat_user_indexes WHERE relname = '%s';
SELECT indexname, indexdef FROM pg_indexes WHERE tablename = '%s';`, args.Table, args.Table), ""
		}
		return fmt.Sprintf(`SELECT index_name, column_name, cardinality FROM information_schema.statistics WHERE table_name = '%s';`, args.Table), ""
	default:
		return "", fmt.Sprintf("invalid operation %q; expected explain, table_size or table_indexes", args.Operation)
	}
}

// runExec runs a read-only metadata script as an auditable plain-exec session,
// mirroring the schema explorer pattern.
func (e *gatewayToolExecutor) runExec(script string) (string, error) {
	sessionID := uuid.NewString()
	userAgent := e.exec.UserAgent
	if userAgent == "" {
		userAgent = "aianalyzer.metadata"
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
	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), metadataExecTimeout)
	defer cancelFn()
	select {
	case resp := <-respCh:
		if resp.ExitCode != 0 && resp.ExitCode != -2 {
			return "", fmt.Errorf("exit_code=%d output=%s", resp.ExitCode, resp.Output)
		}
		return resp.Output, nil
	case <-timeoutCtx.Done():
		client.Close()
		return "", fmt.Errorf("metadata query timed out after %s", metadataExecTimeout)
	}
}

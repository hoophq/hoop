package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const schemaTimeout = 30 * time.Second

type connectionDatabasesInput struct {
	ConnectionName string `json:"connection_name" jsonschema:"name of the database connection"`
}

type connectionTablesInput struct {
	ConnectionName string `json:"connection_name" jsonschema:"name of the database connection"`
	Database       string `json:"database" jsonschema:"name of the database to list tables from (required for Postgres, MSSQL, MySQL, MongoDB)"`
}

type connectionColumnsInput struct {
	ConnectionName string `json:"connection_name" jsonschema:"name of the database connection"`
	Database       string `json:"database" jsonschema:"name of the database (required for Postgres, MSSQL, MySQL, MongoDB)"`
	Table          string `json:"table" jsonschema:"name of the table"`
	Schema         string `json:"schema,omitempty" jsonschema:"schema name (optional; defaults: public for Postgres, dbo for MSSQL, database name otherwise)"`
}

func registerSchemaTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name: "connection_databases",
		Description: "List databases available on a Hoop database connection (Postgres, MySQL, MSSQL, MongoDB). " +
			"Returns the raw query output; agents can parse database names from it.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, connectionDatabasesHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "connection_tables",
		Description: "List tables in a given database on a Hoop database connection. " +
			"Returns the raw query output.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, connectionTablesHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "connection_columns",
		Description: "List columns of a specific table on a Hoop database connection, with types. " +
			"Returns the raw query output.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, connectionColumnsHandler)
}

func connectionDatabasesHandler(ctx context.Context, _ *mcp.CallToolRequest, args connectionDatabasesInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	conn, token, err := resolveSchemaRequest(ctx, args.ConnectionName)
	if err != nil {
		return err.toResult(), nil, nil
	}

	connType := pb.ToConnectionType(conn.Type, conn.SubType.String)
	script := databasesScriptFor(connType)
	if script == "" {
		return errResult(fmt.Sprintf("listing databases is not supported for connection type %q", connType)), nil, nil
	}

	output, timedOut, err2 := runSchemaExec(sc.GetOrgID(), conn.Name, script, token)
	if err2 != nil {
		return nil, nil, err2
	}
	if timedOut {
		return errResult(fmt.Sprintf("timed out (%s) listing databases", schemaTimeout)), nil, nil
	}
	return jsonResult(map[string]any{
		"connection_name":    conn.Name,
		"connection_subtype": conn.SubType.String,
		"output":             output,
	})
}

func connectionTablesHandler(ctx context.Context, _ *mcp.CallToolRequest, args connectionTablesInput) (*mcp.CallToolResult, any, error) {
	if args.Database == "" {
		return errResult("database is required"), nil, nil
	}
	sc := storageContextFrom(ctx)
	conn, token, err := resolveSchemaRequest(ctx, args.ConnectionName)
	if err != nil {
		return err.toResult(), nil, nil
	}

	connType := pb.ToConnectionType(conn.Type, conn.SubType.String)
	script := apiconnections.TablesQueryFor(connType, args.Database)
	if script == "" {
		return errResult(fmt.Sprintf("listing tables is not supported for connection type %q", connType)), nil, nil
	}

	output, timedOut, err2 := runSchemaExec(sc.GetOrgID(), conn.Name, script, token)
	if err2 != nil {
		return nil, nil, err2
	}
	if timedOut {
		return errResult(fmt.Sprintf("timed out (%s) listing tables", schemaTimeout)), nil, nil
	}
	return jsonResult(map[string]any{
		"connection_name":    conn.Name,
		"connection_subtype": conn.SubType.String,
		"database":           args.Database,
		"output":             output,
	})
}

func connectionColumnsHandler(ctx context.Context, _ *mcp.CallToolRequest, args connectionColumnsInput) (*mcp.CallToolResult, any, error) {
	if args.Table == "" {
		return errResult("table is required"), nil, nil
	}
	sc := storageContextFrom(ctx)
	conn, token, err := resolveSchemaRequest(ctx, args.ConnectionName)
	if err != nil {
		return err.toResult(), nil, nil
	}

	connType := pb.ToConnectionType(conn.Type, conn.SubType.String)
	schema := args.Schema
	if schema == "" {
		switch connType {
		case pb.ConnectionTypePostgres:
			schema = "public"
		case pb.ConnectionTypeMSSQL:
			schema = "dbo"
		default:
			schema = args.Database
		}
	}
	script := apiconnections.ColumnsQueryFor(connType, args.Database, args.Table, schema)
	if script == "" {
		return errResult(fmt.Sprintf("listing columns is not supported for connection type %q", connType)), nil, nil
	}

	output, timedOut, err2 := runSchemaExec(sc.GetOrgID(), conn.Name, script, token)
	if err2 != nil {
		return nil, nil, err2
	}
	if timedOut {
		return errResult(fmt.Sprintf("timed out (%s) listing columns", schemaTimeout)), nil, nil
	}
	return jsonResult(map[string]any{
		"connection_name":    conn.Name,
		"connection_subtype": conn.SubType.String,
		"database":           args.Database,
		"table":              args.Table,
		"schema":             schema,
		"output":             output,
	})
}

type schemaErr struct{ msg string }

func (e *schemaErr) toResult() *mcp.CallToolResult { return errResult(e.msg) }

func resolveSchemaRequest(ctx context.Context, connectionName string) (*models.Connection, string, *schemaErr) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, "", &schemaErr{"unauthorized: missing auth context"}
	}
	token := accessTokenFrom(ctx)
	if token == "" {
		return nil, "", &schemaErr{"unauthorized: missing access token"}
	}
	if connectionName == "" {
		return nil, "", &schemaErr{"connection_name is required"}
	}
	conn, err := models.GetConnectionByNameOrID(sc, connectionName)
	if err != nil {
		return nil, "", &schemaErr{fmt.Sprintf("failed looking up connection: %v", err)}
	}
	if conn == nil {
		return nil, "", &schemaErr{fmt.Sprintf("connection %q not found or not accessible", connectionName)}
	}
	isDatabase := conn.Type == "database"
	if !isDatabase {
		return nil, "", &schemaErr{fmt.Sprintf("connection %q is not a database type", connectionName)}
	}
	return conn, token, nil
}

func databasesScriptFor(connType pb.ConnectionType) string {
	switch connType {
	case pb.ConnectionTypePostgres:
		return `SELECT datname AS database_name FROM pg_database
WHERE datistemplate = false AND datname != 'postgres' AND datname != 'rdsadmin'
ORDER BY datname;`
	case pb.ConnectionTypeMySQL:
		return `SELECT schema_name AS database_name FROM information_schema.schemata ORDER BY schema_name;`
	case pb.ConnectionTypeMSSQL:
		return `SET NOCOUNT ON; SELECT name FROM sys.databases WHERE name != 'rdsadmin'`
	case pb.ConnectionTypeMongoDB:
		return `var dbs = db.adminCommand('listDatabases');
var result = [];
dbs.databases.forEach(function(d) { result.push({ "database_name": d.name }); });
print(JSON.stringify(result));`
	default:
		return ""
	}
}

func runSchemaExec(orgID, connectionName, script, token string) (string, bool, error) {
	sessionID := uuid.NewString()
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          orgID,
		SessionID:      sessionID,
		ConnectionName: connectionName,
		BearerToken:    token,
		UserAgent:      "mcp.schema",
		Verb:           pb.ClientVerbPlainExec,
	})
	if err != nil {
		return "", false, fmt.Errorf("failed creating exec client: %w", err)
	}
	respCh := make(chan *clientexec.Response, 1)
	go func() {
		defer func() { close(respCh); client.Close() }()
		respCh <- client.Run([]byte(script), nil)
	}()
	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), schemaTimeout)
	defer cancelFn()
	select {
	case resp := <-respCh:
		if resp.ExitCode != 0 && resp.ExitCode != -2 {
			log.With("sid", sessionID).Warnf("schema exec non-zero exit, %v", resp.String())
			return "", false, fmt.Errorf("schema query failed: %s", resp.Output)
		}
		return resp.Output, false, nil
	case <-timeoutCtx.Done():
		client.Close()
		return "", true, nil
	}
}

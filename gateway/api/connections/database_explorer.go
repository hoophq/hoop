package apiconnections

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// ListDatabases return a list of databases for a given connection
//
//	@Summary		List Databases
//	@Description	List all available databases for a database connection
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Success		200			{object}	openapi.ConnectionDatabaseListResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/databases [get]
func ListDatabases(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")

	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	if conn.Type != "database" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection is not a database type"})
		return
	}

	currentConnectionType := pb.ToConnectionType(conn.Type, conn.SubType.String)
	var script string
	switch currentConnectionType {
	case pb.ConnectionTypePostgres:
		script = `
SELECT datname as database_name FROM pg_database 
WHERE datistemplate = false AND datname != 'postgres' AND datname != 'rdsadmin'
ORDER BY datname;`

	case pb.ConnectionTypeMongoDB:
		script = `
// if (typeof noVerbose === 'function') noVerbose();
// if (typeof config !== 'undefined') config.verbosity = 0;

var dbs = db.adminCommand('listDatabases');
var result = [];
dbs.databases.forEach(function(database) {
	result.push({
					"database_name": database.name
	});
});
print(JSON.stringify(result));`
	case pb.ConnectionTypeMySQL:
		script = `SELECT schema_name AS database_name FROM information_schema.schemata ORDER BY schema_name;`
	case pb.ConnectionTypeMSSQL:
		script = `SET NOCOUNT ON; SELECT name FROM sys.databases WHERE name != 'rdsadmin'`
	default:
		log.Warnf("unsupported database type: %v", currentConnectionType)
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:                     ctx.GetOrgID(),
		ConnectionName:            conn.Name,
		ConnectionCommandOverride: getConnectionCommandOverride(currentConnectionType, conn.Command),
		BearerToken:               apiroutes.GetAccessTokenFromRequest(c),
		UserAgent:                 userAgent,
		// it sets the execution to perform plain executions
		Verb: pb.ClientVerbPlainExec,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(script), nil):
		default:
		}
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		if outcome.ExitCode != 0 {
			log.Errorf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("command failed: %s", outcome.Output)})
			return
		}

		var databases []string
		var err error

		switch currentConnectionType {
		case pb.ConnectionTypeMongoDB:
			var result []map[string]any
			if output := cleanMongoOutput(outcome.Output); output != "" {
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					log.Errorf("failed parsing mongo response: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse MongoDB response: %v", err)})
					return
				}
			}
			for _, db := range result {
				if dbName, ok := db["database_name"].(string); ok {
					databases = append(databases, dbName)
				}
			}
		default:
			databases, err = parseDatabaseCommandOutput(outcome.Output)
			if err != nil {
				log.Errorf("failed parsing command output response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse response: %v", err)})
				return
			}
		}
		c.JSON(http.StatusOK, openapi.ConnectionDatabaseListResponse{Databases: databases})
	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runexec timeout (50s), it will return async")
		c.JSON(http.StatusBadRequest, gin.H{"message": "Request timed out"})
	}
}

// ListTables returns only the tables of a database without column details
//
//	@Summary		List Database Tables
//	@Description	List tables from a database without column details
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID		path		string	true	"Name or UUID of the connection"
//	@Param			database		query		string	true	"Name of the database"
//	@Param			schema			query		string	false	"Name of the schema (optional - if not provided, returns tables from all schemas)"
//	@Success		200				{object}	openapi.TablesResponse
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/tables [get]
func ListTables(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	dbName := c.Query("database")
	schemaName := c.Query("schema")

	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	isDatabaseConnection := conn.Type == "database" ||
		(conn.Type == "custom" && conn.SubType.String == "dynamodb") ||
		(conn.Type == "custom" && conn.SubType.String == "cloudwatch")
	if !isDatabaseConnection {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection is not a database type"})
		return
	}

	currentConnectionType := pb.ToConnectionType(conn.Type, conn.SubType.String)

	// Verify if dbName is needed (except for DynamoDB)
	needsDbName := currentConnectionType == pb.ConnectionTypePostgres ||
		currentConnectionType == pb.ConnectionTypeMSSQL ||
		currentConnectionType == pb.ConnectionTypeMySQL ||
		currentConnectionType == pb.ConnectionTypeMongoDB

	// DynamoDB doesn't need dbName
	if conn.Type == "custom" && conn.SubType.String == "dynamodb" ||
		conn.Type == "custom" && conn.SubType.String == "cloudwatch" {
		needsDbName = false
	}

	// For database types that require dbName
	if needsDbName {
		// Check if provided
		if dbName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "database parameter is required for this database type"})
			return
		}

		// Validate format
		if err := validateDatabaseName(dbName); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
	}

	script := getTablesQuery(currentConnectionType, dbName)
	if script == "" {
		// Check for DynamoDB
		if conn.Type == "custom" && conn.SubType.String == "dynamodb" {
			script = `aws dynamodb list-tables --output json`
		}
	}

	if script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:                     ctx.GetOrgID(),
		ConnectionName:            conn.Name,
		ConnectionCommandOverride: getConnectionCommandOverride(currentConnectionType, conn.Command),
		BearerToken:               apiroutes.GetAccessTokenFromRequest(c),
		UserAgent:                 userAgent,
		Verb:                      pb.ClientVerbPlainExec,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(script), nil):
		default:
		}
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		if outcome.ExitCode != 0 {
			log.Warnf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("failed to list tables: %s", outcome.Output)})
			return
		}

		var tables openapi.TablesResponse
		switch currentConnectionType {
		case pb.ConnectionTypeDynamoDB:
			tables, err = parseDynamoDBTables(outcome.Output)
		case pb.ConnectionTypeCloudWatch:
			tables, err = parseCloudWatchTables(outcome.Output)
		case pb.ConnectionTypeMongoDB:
			tables, err = parseMongoDBTables(outcome.Output)
		default:
			tables, err = parseSQLTables(outcome.Output, currentConnectionType)

			// If a specific schema was requested, filter the results
			if schemaName != "" {
				filteredSchemas := []openapi.SchemaInfo{}
				for _, schema := range tables.Schemas {
					if schema.Name == schemaName {
						filteredSchemas = append(filteredSchemas, schema)
						break
					}
				}
				tables.Schemas = filteredSchemas
			}
		}

		if err != nil {
			errMsg := fmt.Sprintf("failed to parse %v response: %v", currentConnectionType, err)
			log.Error(errMsg)
			c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
			return
		}
		c.JSON(http.StatusOK, tables)
	case <-timeoutCtx.Done():
		client.Close()
		log.Warnf("timeout (30s) obtaining tables for database '%s' using connection '%s'", dbName, conn.Name)
		c.JSON(http.StatusBadRequest, gin.H{
			"message":    fmt.Sprintf("Request timed out (30s) while fetching tables for database '%s'", dbName),
			"connection": conn.Name,
			"database":   dbName,
			"timeout":    "30s",
		})
	}
}

// GetTableColumns returns the columns of a specific table
//
//	@Summary		Get Table Columns
//	@Description	Get columns from a specific table
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID		path		string	true	"Name or UUID of the connection"
//	@Param			database		query		string	true	"Name of the database"
//	@Param			table			query		string	true	"Name of the table"
//	@Param			schema			query		string	false	"Name of the schema (optional - for PostgreSQL default is 'public', for others defaults to database name)"
//	@Success		200				{object}	openapi.ColumnsResponse
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/columns [get]
func GetTableColumns(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	dbName := c.Query("database")
	tableName := c.Query("table")
	schemaName := c.Query("schema")

	if tableName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "table parameter is required"})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	isDatabaseConnection := conn.Type == "database" ||
		(conn.Type == "custom" && conn.SubType.String == "dynamodb")
	if !isDatabaseConnection {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection is not a database type"})
		return
	}

	currentConnectionType := pb.ToConnectionType(conn.Type, conn.SubType.String)

	// Verify if dbName is needed (except for DynamoDB)
	needsDbName := currentConnectionType == pb.ConnectionTypePostgres ||
		currentConnectionType == pb.ConnectionTypeMySQL ||
		currentConnectionType == pb.ConnectionTypeMongoDB

	// DynamoDB doesn't need dbName
	if currentConnectionType == pb.ConnectionTypeDynamoDB {
		needsDbName = false
	}

	// For database types that require dbName
	if needsDbName {
		// Check if provided
		if dbName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "database parameter is required for this database type"})
			return
		}

		// Validate format
		if err := validateDatabaseName(dbName); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
	}

	if schemaName == "" {
		schemaName = dbName
		if currentConnectionType == pb.ConnectionTypePostgres {
			schemaName = "public"
		}
	}

	script := getColumnsQuery(currentConnectionType, dbName, tableName, schemaName)
	if script == "" {
		// Check for DynamoDB
		if currentConnectionType == pb.ConnectionTypeDynamoDB {
			script = fmt.Sprintf(`aws dynamodb describe-table --table-name %s --output json`, tableName)
		}
	}

	if script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		ConnectionName: conn.Name,
		BearerToken:    apiroutes.GetAccessTokenFromRequest(c),
		UserAgent:      userAgent,
		Verb:           pb.ClientVerbPlainExec,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(script), nil):
		default:
		}
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		if outcome.ExitCode != 0 {
			log.Warnf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("failed to get columns: %s", outcome.Output)})
			return
		}

		response := openapi.ColumnsResponse{Columns: []openapi.ConnectionColumn{}}
		switch currentConnectionType {
		case pb.ConnectionTypeDynamoDB:
			response.Columns, err = parseDynamoDBColumns(outcome.Output)
		case pb.ConnectionTypeMongoDB:
			response.Columns, err = parseMongoDBColumns(outcome.Output)
		default:
			response.Columns, err = parseSQLColumns(outcome.Output, currentConnectionType)
		}
		if err != nil {
			log.Errorf("failed parsing columns response, type=%v, err=%v", currentConnectionType, err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse columns response: %v", err)})
			return
		}
		c.JSON(http.StatusOK, response)
	case <-timeoutCtx.Done():
		client.Close()
		log.Warnf("timeout (30s) obtaining columns for table '%s' in database '%s' using connection '%s'", tableName, dbName, conn.Name)
		c.JSON(http.StatusBadRequest, gin.H{
			"message":    fmt.Sprintf("Request timed out (30s) while fetching columns for table '%s'", tableName),
			"connection": conn.Name,
			"database":   dbName,
			"table":      tableName,
			"timeout":    "30s",
		})
	}
}

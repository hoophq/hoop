package apiconnections

import (
	"fmt"

	pb "github.com/hoophq/hoop/common/proto"
)

func getSchemaQuery(connType pb.ConnectionType, dbName string) string {
	switch connType {
	case pb.ConnectionTypePostgres:
		return getPostgresSchemaQuery(dbName)
	case pb.ConnectionTypeMSSQL:
		return getMSSQLSchemaQuery()
	case pb.ConnectionTypeMySQL:
		return getMySQLSchemaQuery()
	case pb.ConnectionTypeOracleDB:
		return getOracleDBSchemaQuery()
	case pb.ConnectionTypeMongoDB:
		return getMongoDBSchemaQuery(dbName)
	default:
		return ""
	}
}

// getTablesQuery retorna a consulta para listar apenas as tabelas de um banco
func getTablesQuery(connType pb.ConnectionType, dbName string) string {
	switch connType {
	case pb.ConnectionTypePostgres:
		return getPostgresTablesQuery(dbName)
	case pb.ConnectionTypeMSSQL:
		return getMSSQLTablesQuery()
	case pb.ConnectionTypeMySQL:
		return getMySQLTablesQuery()
	case pb.ConnectionTypeOracleDB:
		return getOracleDBTablesQuery()
	case pb.ConnectionTypeMongoDB:
		return getMongoDBTablesQuery(dbName)
	default:
		return ""
	}
}

// getColumnsQuery retorna a consulta para obter as colunas de uma tabela específica
func getColumnsQuery(connType pb.ConnectionType, dbName, tableName, schemaName string) string {
	switch connType {
	case pb.ConnectionTypePostgres:
		return getPostgresColumnsQuery(dbName, tableName, schemaName)
	case pb.ConnectionTypeMSSQL:
		return getMSSQLColumnsQuery(tableName, schemaName)
	case pb.ConnectionTypeMySQL:
		return getMySQLColumnsQuery(tableName, schemaName)
	case pb.ConnectionTypeOracleDB:
		return getOracleDBColumnsQuery(tableName, schemaName)
	case pb.ConnectionTypeMongoDB:
		return getMongoDBColumnsQuery(dbName, tableName)
	default:
		return ""
	}
}

func getPostgresSchemaQuery(dbName string) string {
	return fmt.Sprintf(`
    \set QUIET on
    \c %s
    \set QUIET off
SELECT
    c.table_schema as schema_name,
    'table' as object_type,
    c.table_name as object_name,
    c.column_name,
    CASE
        WHEN c.data_type = 'character varying' THEN
            c.data_type || '(' || c.character_maximum_length || ')'
        WHEN c.data_type = 'numeric' AND c.numeric_precision IS NOT NULL THEN
            CASE
                WHEN c.numeric_scale = 0 THEN
                    'numeric(' || c.numeric_precision || ')'
                ELSE
                    'numeric(' || c.numeric_precision || ',' || c.numeric_scale || ')'
            END
        ELSE c.data_type
    END as column_type,
    c.is_nullable = 'NO' as not_null
FROM information_schema.columns c
JOIN information_schema.tables t
    ON c.table_schema = t.table_schema
    AND c.table_name = t.table_name
WHERE t.table_type = 'BASE TABLE'
AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
ORDER BY
    c.table_schema,
    c.table_name,
    c.ordinal_position;`, dbName)
}

// Consultas para listar apenas tabelas

func getPostgresTablesQuery(dbName string) string {
	return fmt.Sprintf(`
    \set QUIET on
    \c %s
    \set QUIET off
SELECT
    table_schema as schema_name,
    'table' as object_type,
    table_name as object_name
FROM information_schema.tables
WHERE table_type = 'BASE TABLE'
AND table_schema NOT IN ('pg_catalog', 'information_schema')
ORDER BY table_schema, table_name;`, dbName)
}

func getMSSQLTablesQuery() string {
	return `
SET NOCOUNT ON;
SELECT
    s.name as schema_name,
    'table' as object_type,
    o.name as object_name
FROM sys.schemas s
JOIN sys.objects o ON o.schema_id = s.schema_id
WHERE o.type = 'U'  -- U for user-defined tables only
ORDER BY s.name, o.name;`
}

func getMySQLTablesQuery() string {
	return `
SELECT
    t.TABLE_SCHEMA as schema_name,
    'table' as object_type,
    t.TABLE_NAME as object_name
FROM INFORMATION_SCHEMA.TABLES t
WHERE t.TABLE_SCHEMA NOT IN ('information_schema', 'performance_schema', 'mysql', 'pg_catalog', 'sys')
AND t.TABLE_TYPE = 'BASE TABLE'
ORDER BY t.TABLE_SCHEMA, t.TABLE_NAME;`
}

func getOracleDBTablesQuery() string {
	return `
SELECT
    t.owner as schema_name,
    'table' as object_type,
    t.table_name as object_name
FROM all_tables t
WHERE t.owner NOT IN (
    'SYS', 'SYSTEM', 'SYSMAN', 'MGMT_VIEW', 'OJVMSYS',
    'OUTLN', 'DBSNMP', 'APPQOSSYS', 'APEX_030200', 'APEX_040000',
    'APEX_PUBLIC_USER', 'APEX_REST_PUBLIC_USER', 'CTXSYS', 'ANONYMOUS',
    'FLOWS_FILES', 'MDSYS', 'OLAPSYS', 'ORDDATA', 'ORDSYS', 'SI_INFORMTN_SCHEMA',
    'WMSYS', 'XDB', 'EXFSYS', 'ORDPLUGINS', 'OWBSYS', 'OWBSYS_AUDIT',
    'ORACLE_OCM', 'SPATIAL_CSW_ADMIN_USR', 'SPATIAL_WFS_ADMIN_USR', 'DVSYS',
    'AUDSYS', 'GSMADMIN_INTERNAL', 'LBACSYS', 'REMOTE_SCHEDULER_AGENT',
    'SYSBACKUP', 'SYSDG', 'SYSKM', 'GSMUSER', 'SYSRAC'
)
ORDER BY t.owner, t.table_name;`
}

func getMongoDBTablesQuery(dbName string) string {
	return fmt.Sprintf(`
// Ensure verbosity is off
if (typeof noVerbose === 'function') noVerbose();
if (typeof config !== 'undefined') config.verbosity = 0;

var result = [];
var dbName = '%s';

db.getSiblingDB(dbName).getCollectionNames().forEach(function(collName) {
    result.push({
        schema_name: dbName,
        object_type: 'table',
        object_name: collName
    });
});

print(JSON.stringify(result));`, dbName)
}

// Consultas para obter colunas de uma tabela específica

func getPostgresColumnsQuery(dbName, tableName, schemaName string) string {
	return fmt.Sprintf(`
    \set QUIET on
    \c %s
    \set QUIET off
SELECT
    c.column_name,
    CASE
        WHEN c.data_type = 'character varying' THEN
            c.data_type || '(' || c.character_maximum_length || ')'
        WHEN c.data_type = 'numeric' AND c.numeric_precision IS NOT NULL THEN
            CASE
                WHEN c.numeric_scale = 0 THEN
                    'numeric(' || c.numeric_precision || ')'
                ELSE
                    'numeric(' || c.numeric_precision || ',' || c.numeric_scale || ')'
            END
        ELSE c.data_type
    END as column_type,
    c.is_nullable = 'NO' as not_null
FROM information_schema.columns c
WHERE c.table_schema = '%s' AND c.table_name = '%s'
ORDER BY c.ordinal_position;`, dbName, schemaName, tableName)
}

func getMSSQLColumnsQuery(tableName, schemaName string) string {
	return fmt.Sprintf(`
SET NOCOUNT ON;
SELECT
    c.name as column_name,
    CASE
        WHEN t.name = 'varchar' AND c.max_length != -1 THEN
            t.name + '(' + CAST(c.max_length AS VARCHAR) + ')'
        WHEN t.name = 'decimal' AND c.precision != 0 THEN
            t.name + '(' + CAST(c.precision AS VARCHAR) + ',' + CAST(c.scale AS VARCHAR) + ')'
        ELSE t.name
    END as column_type,
    c.is_nullable as not_null
FROM sys.schemas s
JOIN sys.objects o ON o.schema_id = s.schema_id
JOIN sys.columns c ON o.object_id = c.object_id
JOIN sys.types t ON c.user_type_id = t.user_type_id
WHERE s.name = '%s' AND o.name = '%s' AND o.type = 'U'
ORDER BY c.column_id;`, schemaName, tableName)
}

func getMySQLColumnsQuery(tableName, schemaName string) string {
	return fmt.Sprintf(`
SELECT
    c.COLUMN_NAME as column_name,
    CASE
        WHEN c.DATA_TYPE = 'varchar' THEN
            CONCAT(c.DATA_TYPE, '(', c.CHARACTER_MAXIMUM_LENGTH, ')')
        WHEN c.DATA_TYPE = 'decimal' AND c.NUMERIC_PRECISION IS NOT NULL THEN
            CONCAT(c.DATA_TYPE, '(', c.NUMERIC_PRECISION, ',', c.NUMERIC_SCALE, ')')
        ELSE c.DATA_TYPE
    END as column_type,
    c.IS_NULLABLE = 'NO' as not_null
FROM INFORMATION_SCHEMA.COLUMNS c
WHERE c.TABLE_SCHEMA = '%s' AND c.TABLE_NAME = '%s'
ORDER BY c.ORDINAL_POSITION;`, schemaName, tableName)
}

func getOracleDBColumnsQuery(tableName, schemaName string) string {
	return fmt.Sprintf(`
SELECT
    c.column_name,
    c.data_type as column_type,
    CASE WHEN c.nullable = 'Y' THEN '0' ELSE '1' END as not_null
FROM all_tab_columns c
WHERE c.owner = '%s' AND c.table_name = '%s'
ORDER BY c.column_id;`, schemaName, tableName)
}

func getMongoDBColumnsQuery(dbName, tableName string) string {
	return fmt.Sprintf(`
// Ensure verbosity is off
if (typeof noVerbose === 'function') noVerbose();
if (typeof config !== 'undefined') config.verbosity = 0;

var result = [];
var dbName = '%s';
var collName = '%s';

var coll = db.getSiblingDB(dbName).getCollection(collName);
var sample = coll.findOne();

function getSchemaFromDoc(doc, prefix = '') {
    var schema = {};
    Object.keys(doc || {}).forEach(function(key) {
        var fullKey = prefix ? prefix + '.' + key : key;
        var value = doc[key];

        if (value === null) {
            schema[fullKey] = {
                type: 'null',
                nullable: true
            };
        } else if (Array.isArray(value)) {
            schema[fullKey] = {
                type: 'array',
                nullable: false
            };
        } else if (typeof value === 'object' && !(value instanceof ObjectId) && !(value instanceof Date)) {
            Object.assign(schema, getSchemaFromDoc(value, fullKey));
        } else {
            schema[fullKey] = {
                type: value instanceof ObjectId ? 'objectId' :
                      value instanceof Date ? 'date' :
                      typeof value,
                nullable: false
            };
        }
    });
    return schema;
}

var collSchema = getSchemaFromDoc(sample);
Object.keys(collSchema).forEach(function(field) {
    result.push({
        column_name: field,
        column_type: collSchema[field].type,
        not_null: !collSchema[field].nullable
    });
});

print(JSON.stringify(result));`, dbName, tableName)
}

func getMSSQLSchemaQuery() string {
	return `
SET NOCOUNT ON;
SELECT
    s.name as schema_name,
    'table' as object_type,
    o.name as object_name,
    c.name as column_name,
    CASE
        WHEN t.name = 'varchar' AND c.max_length != -1 THEN
            t.name + '(' + CAST(c.max_length AS VARCHAR) + ')'
        WHEN t.name = 'decimal' AND c.precision != 0 THEN
            t.name + '(' + CAST(c.precision AS VARCHAR) + ',' + CAST(c.scale AS VARCHAR) + ')'
        ELSE t.name
    END as column_type,
    c.is_nullable as not_null
FROM sys.schemas s
JOIN sys.objects o ON o.schema_id = s.schema_id
JOIN sys.columns c ON o.object_id = c.object_id
JOIN sys.types t ON c.user_type_id = t.user_type_id
WHERE o.type = 'U'  -- U for user-defined tables only
ORDER BY
    s.name,
    o.name,
    c.column_id;`
}

func getMySQLSchemaQuery() string {
	return `
SELECT
    c.TABLE_SCHEMA as schema_name,
    'table' as object_type,
    c.TABLE_NAME as object_name,
    c.COLUMN_NAME as column_name,
    CASE
        WHEN c.DATA_TYPE = 'varchar' THEN
            CONCAT(c.DATA_TYPE, '(', c.CHARACTER_MAXIMUM_LENGTH, ')')
        WHEN c.DATA_TYPE = 'decimal' AND c.NUMERIC_PRECISION IS NOT NULL THEN
            CONCAT(c.DATA_TYPE, '(', c.NUMERIC_PRECISION, ',', c.NUMERIC_SCALE, ')')
        ELSE c.DATA_TYPE
    END as column_type,
    c.IS_NULLABLE = 'NO' as not_null
FROM INFORMATION_SCHEMA.COLUMNS c
JOIN INFORMATION_SCHEMA.TABLES t
    ON c.TABLE_SCHEMA = t.TABLE_SCHEMA
    AND c.TABLE_NAME = t.TABLE_NAME
WHERE c.TABLE_SCHEMA NOT IN ('information_schema', 'performance_schema', 'mysql', 'pg_catalog', 'sys')
AND t.TABLE_TYPE = 'BASE TABLE'
ORDER BY
    c.TABLE_SCHEMA,
    c.TABLE_NAME,
    c.ORDINAL_POSITION;`
}

func getOracleDBSchemaQuery() string {
	return `
SELECT
    t.owner as schema_name,
    'table' as object_type,
    t.table_name as object_name,
    c.column_name,
    c.data_type as column_type,
CASE WHEN c.nullable = 'Y' THEN '0' ELSE '1' END as not_null FROM all_tables t
JOIN all_tab_columns c
ON t.table_name = c.table_name AND t.owner = c.owner WHERE t.owner NOT IN (
    'SYS', 'SYSTEM', 'SYSMAN', 'MGMT_VIEW', 'OJVMSYS',
    'OUTLN', 'DBSNMP', 'APPQOSSYS', 'APEX_030200', 'APEX_040000',
    'APEX_PUBLIC_USER', 'APEX_REST_PUBLIC_USER', 'CTXSYS', 'ANONYMOUS',
    'FLOWS_FILES', 'MDSYS', 'OLAPSYS', 'ORDDATA', 'ORDSYS', 'SI_INFORMTN_SCHEMA',
    'WMSYS', 'XDB', 'EXFSYS', 'ORDPLUGINS', 'OWBSYS', 'OWBSYS_AUDIT',
    'ORACLE_OCM', 'SPATIAL_CSW_ADMIN_USR', 'SPATIAL_WFS_ADMIN_USR', 'DVSYS',
    'AUDSYS', 'GSMADMIN_INTERNAL', 'LBACSYS', 'REMOTE_SCHEDULER_AGENT',
    'SYSBACKUP', 'SYSDG', 'SYSKM', 'GSMUSER', 'SYSRAC'
)
ORDER BY
    t.table_name,
    c.column_name;`
}

func getMongoDBSchemaQuery(dbName string) string {
	return fmt.Sprintf(`
// Ensure verbosity is off
if (typeof noVerbose === 'function') noVerbose();
if (typeof config !== 'undefined') config.verbosity = 0;

var result = [];
var dbName = '%s';

db.getSiblingDB(dbName).getCollectionNames().forEach(function(collName) {
    var coll = db.getSiblingDB(dbName).getCollection(collName);
    var sample = coll.findOne();

    function getSchemaFromDoc(doc, prefix = '') {
        var schema = {};
        Object.keys(doc || {}).forEach(function(key) {
            var fullKey = prefix ? prefix + '.' + key : key;
            var value = doc[key];

            if (value === null) {
                schema[fullKey] = {
                    type: 'null',
                    nullable: true
                };
            } else if (Array.isArray(value)) {
                schema[fullKey] = {
                    type: 'array',
                    nullable: false
                };
            } else if (typeof value === 'object' && !(value instanceof ObjectId) && !(value instanceof Date)) {
                Object.assign(schema, getSchemaFromDoc(value, fullKey));
            } else {
                schema[fullKey] = {
                    type: value instanceof ObjectId ? 'objectId' :
                          value instanceof Date ? 'date' :
                          typeof value,
                    nullable: false
                };
            }
        });
        return schema;
    }

    var collSchema = getSchemaFromDoc(sample);
    Object.keys(collSchema).forEach(function(field) {
        result.push({
            schema_name: dbName,
            object_type: 'table',
            object_name: collName,
            column_name: field,
            column_type: collSchema[field].type,
            not_null: !collSchema[field].nullable
        });
    });
});

print(JSON.stringify(result));`, dbName)
}

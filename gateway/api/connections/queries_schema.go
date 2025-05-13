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

// getTablesQuery returns the query to list only the tables of a database
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

// getColumnsQuery returns the query to get the columns of a specific table
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

// Queries to list only tables

func getPostgresTablesQuery(dbName string) string {
	return fmt.Sprintf(`
    \set QUIET on
    \c %s
    \set QUIET off
SELECT
    n.nspname as schema_name,
    CASE c.relkind
        WHEN 'r' THEN 'table'
        WHEN 'v' THEN 'view'
        WHEN 'm' THEN 'materialized_view'
        ELSE c.relkind::text
    END as object_type,
    c.relname as object_name
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'v', 'm')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
  AND n.nspname !~ '^pg_temp_'
ORDER BY n.nspname, c.relname;`, dbName)
}

func getMSSQLTablesQuery() string {
	return `
SET NOCOUNT ON;
SELECT
    s.name as schema_name,
    CASE o.type
        WHEN 'U' THEN 'table'
        WHEN 'V' THEN 'view'
        ELSE o.type
    END as object_type,
    o.name as object_name
FROM sys.schemas s
JOIN sys.objects o ON o.schema_id = s.schema_id
WHERE o.type IN ('U', 'V')  -- U for user-defined tables, V for views
ORDER BY s.name, o.name;`
}

func getMySQLTablesQuery() string {
	return `
SELECT
    t.TABLE_SCHEMA as schema_name,
    CASE t.TABLE_TYPE
        WHEN 'BASE TABLE' THEN 'table'
        WHEN 'VIEW' THEN 'view'
        ELSE LOWER(t.TABLE_TYPE)
    END as object_type,
    t.TABLE_NAME as object_name
FROM INFORMATION_SCHEMA.TABLES t
WHERE t.TABLE_SCHEMA NOT IN ('information_schema', 'performance_schema', 'mysql', 'pg_catalog', 'sys')
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

// Queries to get the columns of a specific table

func getPostgresColumnsQuery(dbName, tableName, schemaName string) string {
	return fmt.Sprintf(`
    \set QUIET on
    \c %s
    \set QUIET off
SELECT
    a.attname as column_name,
    CASE
        WHEN t.typname = 'varchar' THEN 
            'varchar(' || a.atttypmod - 4 || ')'
        WHEN t.typname = 'numeric' AND pg_catalog.format_type(a.atttypid, a.atttypmod) LIKE 'numeric%%' THEN
            pg_catalog.format_type(a.atttypid, a.atttypmod)
        ELSE
            pg_catalog.format_type(a.atttypid, a.atttypmod)
    END as column_type,
    NOT a.attnotnull as "nullable"
FROM
    pg_catalog.pg_attribute a
    JOIN pg_catalog.pg_class c ON a.attrelid = c.oid
    JOIN pg_catalog.pg_namespace n ON c.relnamespace = n.oid
    JOIN pg_catalog.pg_type t ON a.atttypid = t.oid
WHERE
    c.relname = '%s'
    AND n.nspname = '%s'
    AND a.attnum > 0
    AND NOT a.attisdropped
ORDER BY a.attnum;`, dbName, tableName, schemaName)
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
WHERE s.name = '%s' AND o.name = '%s' AND o.type IN ('U', 'V')
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
var samples = coll.find().limit(10).toArray(); // Examine up to 10 documents for a better schema sample

function getMongoType(val) {
    if (val === null) return 'null';
    if (val instanceof ObjectId) return 'objectId';
    if (val instanceof Date) return 'date';
    if (val instanceof NumberLong) return 'numberLong';
    if (val instanceof NumberInt) return 'numberInt';
    if (val instanceof NumberDecimal) return 'decimal';
    if (val instanceof BinData) return 'binary';
    if (Array.isArray(val)) return 'array';
    return typeof val;
}

function getSchemaFromDoc(doc, prefix = '', schema = {}) {
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
            // If array has elements, check first element's type
            if (value.length > 0) {
                schema[fullKey].elementType = getMongoType(value[0]);
            }
        } else if (typeof value === 'object' && !(value instanceof ObjectId) && 
                  !(value instanceof Date) && !(value instanceof NumberLong) && 
                  !(value instanceof NumberInt) && !(value instanceof NumberDecimal) && 
                  !(value instanceof BinData)) {
            getSchemaFromDoc(value, fullKey, schema);
        } else {
            schema[fullKey] = {
                type: getMongoType(value),
                nullable: false
            };
        }
    });
    return schema;
}

// Combine schema from all sample documents
var collSchema = {};
samples.forEach(function(sample) {
    collSchema = getSchemaFromDoc(sample, '', collSchema);
});

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
    var samples = coll.find().limit(10).toArray(); // Examine up to 10 documents for a better schema sample

    function getMongoType(val) {
        if (val === null) return 'null';
        if (val instanceof ObjectId) return 'objectId';
        if (val instanceof Date) return 'date';
        if (val instanceof NumberLong) return 'numberLong';
        if (val instanceof NumberInt) return 'numberInt';
        if (val instanceof NumberDecimal) return 'decimal';
        if (val instanceof BinData) return 'binary';
        if (Array.isArray(val)) return 'array';
        return typeof val;
    }

    function getSchemaFromDoc(doc, prefix = '', schema = {}) {
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
                // If array has elements, check first element's type
                if (value.length > 0) {
                    schema[fullKey].elementType = getMongoType(value[0]);
                }
            } else if (typeof value === 'object' && !(value instanceof ObjectId) && 
                      !(value instanceof Date) && !(value instanceof NumberLong) && 
                      !(value instanceof NumberInt) && !(value instanceof NumberDecimal) && 
                      !(value instanceof BinData)) {
                getSchemaFromDoc(value, fullKey, schema);
            } else {
                schema[fullKey] = {
                    type: getMongoType(value),
                    nullable: false
                };
            }
        });
        return schema;
    }

    // Combine schema from all sample documents
    var collSchema = {};
    samples.forEach(function(sample) {
        collSchema = getSchemaFromDoc(sample, '', collSchema);
    });

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

// getSchemasQuery returns the query to list schemas in a database
func getSchemasQuery(connType pb.ConnectionType, dbName string) string {
	switch connType {
	case pb.ConnectionTypePostgres:
		return getPostgresSchemasQuery(dbName)
	case pb.ConnectionTypeMSSQL:
		return getMSSQLSchemasQuery()
	case pb.ConnectionTypeMySQL:
		return getMySQLSchemasQuery()
	case pb.ConnectionTypeOracleDB:
		return getOracleDBSchemasQuery()
	case pb.ConnectionTypeMongoDB:
		return getMongoDBSchemasQuery(dbName)
	default:
		return ""
	}
}

func getPostgresSchemasQuery(dbName string) string {
	return fmt.Sprintf(`
    \set QUIET on
    \c %s
    \set QUIET off
SELECT 
    nspname as schema_name 
FROM 
    pg_catalog.pg_namespace 
WHERE 
    nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast') 
    AND nspname !~ '^pg_temp_'
ORDER BY 
    nspname;`, dbName)
}

func getMSSQLSchemasQuery() string {
	return `
SET NOCOUNT ON;
SELECT 
    name as schema_name 
FROM 
    sys.schemas 
WHERE 
    name NOT IN ('guest', 'INFORMATION_SCHEMA', 'sys')
ORDER BY 
    name;`
}

func getMySQLSchemasQuery() string {
	return `
SELECT 
    SCHEMA_NAME as schema_name 
FROM 
    INFORMATION_SCHEMA.SCHEMATA 
WHERE 
    SCHEMA_NAME NOT IN ('information_schema', 'performance_schema', 'mysql', 'sys')
ORDER BY 
    SCHEMA_NAME;`
}

func getOracleDBSchemasQuery() string {
	return `
SELECT 
    username as schema_name 
FROM 
    all_users 
WHERE 
    username NOT IN (
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
    username;`
}

func getMongoDBSchemasQuery(dbName string) string {
	return fmt.Sprintf(`
// MongoDB não usa o conceito tradicional de schemas
// Retornamos o próprio banco como schema único
// Ensure verbosity is off
if (typeof noVerbose === 'function') noVerbose();
if (typeof config !== 'undefined') config.verbosity = 0;

var result = [];
result.push({
    "schema_name": "%s"
});
print(JSON.stringify(result));`, dbName)
}

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

func getPostgresSchemaQuery(dbName string) string {
	return fmt.Sprintf(`
    \set QUIET on
    \c %s
    \set QUIET off
WITH pk_info AS (
  SELECT
    c.table_schema,
    c.table_name,
    c.column_name
  FROM information_schema.table_constraints tc
  JOIN information_schema.constraint_column_usage AS ccu USING (constraint_schema, constraint_name)
  JOIN information_schema.columns AS c ON c.table_schema = tc.constraint_schema
    AND tc.table_name = c.table_name AND ccu.column_name = c.column_name
  WHERE constraint_type = 'PRIMARY KEY'
),
fk_info AS (
  SELECT
    tc.table_schema,
    tc.table_name,
    kcu.column_name
  FROM information_schema.table_constraints AS tc
  JOIN information_schema.key_column_usage AS kcu ON tc.constraint_name = kcu.constraint_name
  WHERE tc.constraint_type = 'FOREIGN KEY'
),
index_info AS (
  SELECT
    schemaname,
    tablename,
    indexname,
    array_agg(attname ORDER BY attnum) as columns,
    indisunique as is_unique,
    indisprimary as is_primary
  FROM pg_indexes
  JOIN pg_attribute ON attrelid = (schemaname || '.' || tablename)::regclass
  JOIN pg_index ON indexrelid = (schemaname || '.' || indexname)::regclass
  WHERE attnum = ANY(string_to_array(regexp_replace(indkey::text, '^\{|\}$', '', 'g'), ' ')::smallint[])
  GROUP BY schemaname, tablename, indexname, indisunique, indisprimary
)
SELECT
  n.nspname as schema_name,
  CASE WHEN c.relkind = 'r' THEN 'table' ELSE 'view' END as object_type,
  c.relname as object_name,
  a.attname as column_name,
  format_type(a.atttypid, a.atttypmod) as column_type,
  a.attnotnull as not_null,
  pg_get_expr(d.adbin, d.adrelid) as column_default,
  pk.column_name IS NOT NULL as is_primary_key,
  fk.column_name IS NOT NULL as is_foreign_key,
  i.indexname as index_name,
  i.columns as index_columns,
  i.is_unique as index_is_unique,
  i.is_primary as index_is_primary
FROM pg_catalog.pg_namespace n
JOIN pg_catalog.pg_class c ON c.relnamespace = n.oid
JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid
LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
LEFT JOIN pk_info pk ON pk.table_schema = n.nspname AND pk.table_name = c.relname AND pk.column_name = a.attname
LEFT JOIN fk_info fk ON fk.table_schema = n.nspname AND fk.table_name = c.relname AND fk.column_name = a.attname
LEFT JOIN index_info i ON i.schemaname = n.nspname AND i.tablename = c.relname
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND a.attnum > 0
  AND NOT a.attisdropped
  AND c.relkind IN ('r', 'v')
  AND current_database() = '%s'
ORDER BY schema_name, object_type, object_name, a.attnum;`, dbName, dbName)
}

func getMSSQLSchemaQuery() string {
	return `
SET NOCOUNT ON;
WITH object_info AS (
    SELECT DISTINCT
        s.name as schema_name,
        o.type as object_type,
        o.name as object_name,
        c.name as column_name,
        t.name as column_type,
        c.is_nullable,
        OBJECT_DEFINITION(c.default_object_id) as column_default,
        CASE WHEN pk.column_id IS NOT NULL THEN 1 ELSE 0 END as is_primary_key,
        CASE WHEN fk.parent_column_id IS NOT NULL THEN 1 ELSE 0 END as is_foreign_key
    FROM sys.schemas s
    JOIN sys.objects o ON o.schema_id = s.schema_id
    JOIN sys.columns c ON o.object_id = c.object_id
    JOIN sys.types t ON c.user_type_id = t.user_type_id
    LEFT JOIN (
        SELECT i.object_id, ic.column_id 
        FROM sys.indexes i 
        JOIN sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
        WHERE i.is_primary_key = 1
    ) pk ON pk.object_id = o.object_id AND pk.column_id = c.column_id
    LEFT JOIN sys.foreign_key_columns fk ON fk.parent_object_id = o.object_id AND fk.parent_column_id = c.column_id
    WHERE o.type IN ('U', 'V')
),
index_list AS (
    SELECT 
        OBJECT_SCHEMA_NAME(i.object_id) as schema_name,
        OBJECT_NAME(i.object_id) as object_name,
        c.name as column_name,
        i.name as index_name,
        STRING_AGG(col.name, ',') WITHIN GROUP (ORDER BY ic.key_ordinal) as index_columns,
        i.is_unique,
        i.is_primary_key
    FROM sys.indexes i
    JOIN sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
    JOIN sys.columns c ON ic.object_id = c.object_id AND ic.column_id = c.column_id
    JOIN sys.columns col ON ic.object_id = col.object_id AND ic.column_id = col.column_id
    WHERE OBJECT_SCHEMA_NAME(i.object_id) IS NOT NULL
    GROUP BY 
        OBJECT_SCHEMA_NAME(i.object_id),
        OBJECT_NAME(i.object_id),
        c.name,
        i.name,
        i.is_unique,
        i.is_primary_key
)
SELECT DISTINCT
    o.schema_name,
    CASE WHEN o.object_type = 'U' THEN 'table' ELSE 'view' END,
    o.object_name,
    o.column_name,
    o.column_type,
    o.is_nullable,
    COALESCE(o.column_default, 'NULL'),
    o.is_primary_key,
    o.is_foreign_key,
    COALESCE(i.index_name, 'NULL'),
    COALESCE(i.index_columns, 'NULL'),
    COALESCE(i.is_unique, 0),
    COALESCE(i.is_primary_key, 0)
FROM object_info o
LEFT JOIN index_list i ON o.schema_name = i.schema_name 
    AND o.object_name = i.object_name 
    AND o.column_name = i.column_name
ORDER BY 
    o.schema_name,
    o.object_name,
    o.column_name;`
}

func getMySQLSchemaQuery() string {
	return `
	SELECT 
		c.TABLE_SCHEMA as schema_name,
		CASE WHEN t.TABLE_TYPE = 'BASE TABLE' THEN 'table' ELSE 'view' END as object_type,
		c.TABLE_NAME as object_name,
		c.COLUMN_NAME as column_name,
		c.DATA_TYPE as column_type,
		c.IS_NULLABLE = 'YES' as not_null,
		c.COLUMN_DEFAULT as column_default,
		CASE WHEN c.COLUMN_KEY = 'PRI' THEN 1 ELSE 0 END as is_primary_key,
		CASE WHEN c.COLUMN_KEY = 'MUL' THEN 1 ELSE 0 END as is_foreign_key,
		s.INDEX_NAME as index_name,
		GROUP_CONCAT(s.COLUMN_NAME ORDER BY s.SEQ_IN_INDEX) as index_columns,
		s.NON_UNIQUE = 0 as index_is_unique,
		s.INDEX_NAME = 'PRIMARY' as index_is_primary
	FROM INFORMATION_SCHEMA.COLUMNS c
	JOIN INFORMATION_SCHEMA.TABLES t 
		ON c.TABLE_SCHEMA = t.TABLE_SCHEMA 
		AND c.TABLE_NAME = t.TABLE_NAME
	LEFT JOIN INFORMATION_SCHEMA.STATISTICS s 
		ON c.TABLE_SCHEMA = s.TABLE_SCHEMA 
		AND c.TABLE_NAME = s.TABLE_NAME 
		AND c.COLUMN_NAME = s.COLUMN_NAME
	WHERE c.TABLE_SCHEMA NOT IN ('information_schema', 'performance_schema', 'mysql', 'pg_catalog', 'sys')
	GROUP BY 
		c.TABLE_SCHEMA,
		t.TABLE_TYPE,
		c.TABLE_NAME,
		c.COLUMN_NAME,
		c.DATA_TYPE,
		c.IS_NULLABLE,
		c.COLUMN_DEFAULT,
		c.COLUMN_KEY,
		s.INDEX_NAME,
		s.NON_UNIQUE,
		c.ORDINAL_POSITION
	ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION;`
}

func getOracleDBSchemaQuery() string {
	return `
SELECT 
    t.owner as schema_name,
    CASE WHEN o.object_type = 'TABLE' THEN 'table' ELSE 'view' END as object_type,
    t.table_name as object_name,
    c.column_name,
    c.data_type as column_type,
    CASE WHEN c.nullable = 'Y' THEN '0' ELSE '1' END as not_null,
    CAST(null as VARCHAR2(4000)) as column_default,
    CASE WHEN i.uniqueness = 'UNIQUE' THEN '1' ELSE '0' END as is_primary_key,
    CASE WHEN i.uniqueness = 'NONUNIQUE' THEN '1' ELSE '0' END as is_foreign_key,
    NVL(i.index_name, '') as index_name,
    NVL(LISTAGG(ic.column_name, ',') WITHIN GROUP (ORDER BY ic.column_position), '') as index_columns,
    CASE WHEN i.uniqueness = 'UNIQUE' THEN '1' ELSE '0' END as index_is_unique,
    CASE WHEN i.uniqueness = 'UNIQUE' THEN '1' ELSE '0' END as index_is_primary
FROM all_tables t
JOIN all_tab_columns c 
    ON t.table_name = c.table_name 
    AND t.owner = c.owner
JOIN all_objects o
    ON t.owner = o.owner
    AND t.table_name = o.object_name
LEFT JOIN all_indexes i
    ON t.table_name = i.table_name 
    AND t.owner = i.table_owner
LEFT JOIN all_ind_columns ic
    ON i.index_name = ic.index_name 
    AND i.table_owner = ic.table_owner 
    AND i.table_name = ic.table_name
WHERE t.owner NOT IN (
    'SYS', 'SYSTEM', 'OUTLN', 'DBSNMP', 'XDB', 
    'APEX_040000', 'WMSYS', 'ORDDATA', 'CTXSYS', 'MGMT_VIEW'
)
AND o.object_type IN ('TABLE', 'VIEW')
GROUP BY 
    t.owner,
    o.object_type,
    t.table_name,
    c.column_name,
    c.data_type,
    c.nullable,
    i.index_name,
    i.uniqueness
ORDER BY 
    t.owner,
    t.table_name,
    c.column_name;`
}

func getMongoDBSchemaQuery(dbName string) string {
	return fmt.Sprintf(`
var result = [];
var dbName = '%s';

db.getSiblingDB(dbName).getCollectionNames().forEach(function(collName) {
    var coll = db.getSiblingDB(dbName).getCollection(collName);
    var sample = coll.findOne();
    var indexes = coll.getIndexes();
    
    function getSchemaFromDoc(doc, prefix = '') {
        var schema = {};
        Object.keys(doc || {}).forEach(function(key) {
            var fullKey = prefix ? prefix + '.' + key : key;
            var value = doc[key];
            
            if (value === null) {
                schema[fullKey] = { type: 'null', nullable: true };
            } else if (Array.isArray(value)) {
                schema[fullKey] = { type: 'array', nullable: false };
                if (value.length > 0) {
                    schema[fullKey].items = getSchemaFromDoc(value[0]);
                }
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
    
    // Format as SQL-like schema for consistency
    Object.keys(collSchema).forEach(function(field) {
        result.push({
            schema_name: dbName,
            object_type: 'table',
            object_name: collName,
            column_name: field,
            column_type: collSchema[field].type,
            not_null: !collSchema[field].nullable ? 1 : 0,
            column_default: null,
            is_primary_key: field === '_id' ? 1 : 0,
            is_foreign_key: 0
        });
        
        // Add index information
        indexes.forEach(function(idx) {
            if (idx.key.hasOwnProperty(field)) {
                result[result.length - 1].index_name = idx.name;
                result[result.length - 1].index_columns = Object.keys(idx.key).join(',');
                result[result.length - 1].index_is_unique = idx.unique ? 1 : 0;
                result[result.length - 1].index_is_primary = idx.name === '_id_' ? 1 : 0;
            }
        });
    });
});

JSON.stringify(result);`, dbName)
}

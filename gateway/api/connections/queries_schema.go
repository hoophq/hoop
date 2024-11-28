package apiconnections

import (
	"fmt"

	pb "github.com/hoophq/hoop/common/proto"
)

// getSchemaQuery retorna a query apropriada para cada tipo de banco de dados
func getSchemaQuery(connType pb.ConnectionType, dbName string) string {
	switch connType {
	case pb.ConnectionTypePostgres:
		return getPostgresSchemaQuery(dbName)
	case pb.ConnectionTypeMSSQL:
		return getMSSQLSchemaQuery(dbName)
	case pb.ConnectionTypeMySQL:
		return getMySQLSchemaQuery(dbName)
	case pb.ConnectionTypeOracleDB:
		return getOracleDBSchemaQuery(dbName)
	case pb.ConnectionTypeMongoDB:
		return getMongoDBSchemaQuery(dbName)
	default:
		return ""
	}
}

func getPostgresSchemaQuery(dbName string) string {
	return fmt.Sprintf(`
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
ORDER BY schema_name, object_type, object_name, a.attnum;`, dbName)
}

func getMSSQLSchemaQuery(dbName string) string {
	return fmt.Sprintf(`
SELECT 
    s.name as schema_name,
    CASE WHEN o.type IN ('U') THEN 'table' ELSE 'view' END as object_type,
    o.name as object_name,
    c.name as column_name,
    t.name as column_type,
    c.is_nullable as not_null,
    object_definition(c.default_object_id) as column_default,
    CASE WHEN pk.column_id IS NOT NULL THEN 1 ELSE 0 END as is_primary_key,
    CASE WHEN fk.parent_column_id IS NOT NULL THEN 1 ELSE 0 END as is_foreign_key,
    i.name as index_name,
    STRING_AGG(ic.column_name, ',') WITHIN GROUP (ORDER BY ic.key_ordinal) as index_columns,
    i.is_unique as index_is_unique,
    CASE WHEN i.is_primary_key = 1 THEN 1 ELSE 0 END as index_is_primary
FROM sys.schemas s
JOIN sys.objects o ON o.schema_id = s.schema_id
JOIN sys.columns c ON o.object_id = c.object_id
JOIN sys.types t ON c.user_type_id = t.user_type_id
LEFT JOIN sys.index_columns pk ON pk.object_id = o.object_id 
    AND pk.column_id = c.column_id 
    AND pk.index_id = 1
LEFT JOIN sys.foreign_key_columns fk ON fk.parent_object_id = o.object_id 
    AND fk.parent_column_id = c.column_id
LEFT JOIN sys.indexes i ON o.object_id = i.object_id
LEFT JOIN sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
WHERE o.type IN ('U', 'V')
    AND DB_NAME() = '%s'
GROUP BY s.name, o.type, o.name, c.name, t.name, c.is_nullable, c.default_object_id,
    pk.column_id, fk.parent_column_id, i.name, i.is_unique, i.is_primary_key
ORDER BY schema_name, object_name, column_name;`, dbName)
}

func getMySQLSchemaQuery(dbName string) string {
	return fmt.Sprintf(`
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
	WHERE c.TABLE_SCHEMA = '%s'
			AND c.TABLE_SCHEMA NOT IN ('information_schema', 'performance_schema', 'mysql', 'pg_catalog', 'sys')
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
	ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION;`, dbName)
}

func getOracleDBSchemaQuery(dbName string) string {
	return fmt.Sprintf(`
SELECT 
    owner as schema_name,
    CASE WHEN object_type = 'TABLE' THEN 'table' ELSE 'view' END as object_type,
    object_name as object_name,
    column_name,
    data_type as column_type,
    CASE WHEN nullable = 'N' THEN 1 ELSE 0 END as not_null,
    data_default as column_default,
    CASE WHEN constraint_type = 'P' THEN 1 ELSE 0 END as is_primary_key,
    CASE WHEN constraint_type = 'R' THEN 1 ELSE 0 END as is_foreign_key,
    index_name,
    column_position as index_columns,
    CASE WHEN uniqueness = 'UNIQUE' THEN 1 ELSE 0 END as index_is_unique,
    CASE WHEN constraint_type = 'P' THEN 1 ELSE 0 END as index_is_primary
FROM (
    SELECT 
        c.owner,
        o.object_type,
        c.table_name as object_name,
        c.column_name,
        c.data_type,
        c.nullable,
        c.data_default,
        cc.constraint_type,
        i.index_name,
        ic.column_position,
        i.uniqueness
    FROM all_tab_columns c
    JOIN all_objects o ON c.owner = o.owner 
        AND c.table_name = o.object_name
    LEFT JOIN all_constraints cc ON c.owner = cc.owner 
        AND c.table_name = cc.table_name 
        AND c.column_name = cc.column_name
    LEFT JOIN all_indexes i ON c.owner = i.owner 
        AND c.table_name = i.table_name
    LEFT JOIN all_ind_columns ic ON i.owner = ic.index_owner 
        AND i.index_name = ic.index_name 
        AND c.column_name = ic.column_name
    WHERE c.owner = UPPER('%s')
    AND o.object_type IN ('TABLE', 'VIEW')
)
ORDER BY 
    schema_name,
    object_type,
    object_name,
    column_name;`, dbName)
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

printjson(result);`, dbName)
}

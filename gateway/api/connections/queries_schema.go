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
    TABLE_SCHEMA as schema_name,
    CASE WHEN TABLE_TYPE = 'BASE TABLE' THEN 'table' ELSE 'view' END as object_type,
    TABLE_NAME as object_name,
    COLUMN_NAME as column_name,
    COLUMN_TYPE as column_type,
    IS_NULLABLE = 'YES' as not_null,
    COLUMN_DEFAULT as column_default,
    CASE WHEN COLUMN_KEY = 'PRI' THEN 1 ELSE 0 END as is_primary_key,
    CASE WHEN COLUMN_KEY = 'MUL' THEN 1 ELSE 0 END as is_foreign_key,
    INDEX_NAME as index_name,
    GROUP_CONCAT(INDEX_COLUMN_NAME) as index_columns,
    NON_UNIQUE = 0 as index_is_unique,
    INDEX_NAME = 'PRIMARY' as index_is_primary
FROM information_schema.COLUMNS c
LEFT JOIN information_schema.STATISTICS s 
    ON c.TABLE_SCHEMA = s.TABLE_SCHEMA 
    AND c.TABLE_NAME = s.TABLE_NAME 
    AND c.COLUMN_NAME = s.COLUMN_NAME
WHERE c.TABLE_SCHEMA = '%s'
GROUP BY schema_name, object_type, object_name, column_name, index_name
ORDER BY TABLE_NAME, ORDINAL_POSITION;`, dbName)
}

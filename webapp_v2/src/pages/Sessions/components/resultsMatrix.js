import Papa from 'papaparse'

/**
 * Connection subtypes whose output is parsed as a table.
 * Verbatim from results_container.cljs:68-69.
 */
export const SQL_SUBTYPES = new Set([
  'mysql-csv',
  'mysql',
  'postgres',
  'sql-server',
  'mssql',
  'postgres-csv',
  'sql-server-csv',
  'oracledb',
  'database',
])

/**
 * Port of `transform-results->matrix` (results_container.cljs:12-18).
 *
 * The gateway returns tab-separated output, not commas — hence the explicit
 * delimiter. Oracle prefixes its result with a line the parser must not see.
 */
export function resultsToMatrix(results, connectionSubtype) {
  if (results == null) return undefined
  const input =
    connectionSubtype === 'oracledb' ? results.split('\n').slice(1).join('\n') : results
  return Papa.parse(input, { delimiter: '\t' }).data
}

/**
 * Port of `sanitize-response` (session_details.cljs:30-37): mssql prefixes the
 * payload with a line that is noise. Everything else passes through untouched.
 */
export function sanitizeResponse(results, connectionType) {
  if (connectionType !== 'mssql' || typeof results !== 'string') return results
  const index = results.indexOf('\n')
  return index === -1 ? results : results.slice(index + 1)
}

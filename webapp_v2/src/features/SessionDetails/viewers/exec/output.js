import Papa from 'papaparse'

// Subtypes whose exec output is tab separated and can show as a table.
export const SQL_SUBTYPES = new Set([
  'mysql',
  'mysql-csv',
  'postgres',
  'postgres-csv',
  'sql-server',
  'sql-server-csv',
  'mssql',
  'oracledb',
  'database',
])

// Above this the browser does not build the download file: the gateway does.
export const CLIENT_DOWNLOAD_LIMIT = 2 * 1024 * 1024

// ── Decoding ────────────────────────────────────────────────────────────────

const utf8 = new TextDecoder('utf-8')

// An exec stream is one base64 blob of the output. The gateway writes a tab as
// "∞" in it. Bytes that are not UTF-8 become U+FFFD instead of turning the
// whole output into latin1. When the gateway could not encode the stream it
// sends its error as plain text, which is shown as is.
export function decodeOutput(data) {
  if (!data) return ''
  let binary
  try {
    binary = atob(data)
  } catch {
    return data
  }
  const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0))
  return utf8.decode(bytes).replace(/∞/g, '\t')
}

// Message 5701, printed by USE in the login's language: every translation in
// sys.messages on SQL Server 2022, with %s for the database name. The webapp
// runs `USE [db]` before a mssql script, so its output starts with one.
const DATABASE_CONTEXT_MESSAGES = [
  "Changed database context to '%s'.",
  "已將資料庫內容變更為 '%s'。",
  'Kontext databáze byl změněn na %s.',
  "Databasekonteksten blev ændret til '%s'.",
  'Der Datenbankkontext wurde in "%s" geändert.',
  "Το περιβάλλον της βάσης δεδομένων άλλαξε σε '%s'.",
  'Tietokannan kontekstiksi muutettiin %s.',
  "Le contexte de la base de données a été modifié en '%s'.",
  'Adatbázis-környezet módosítva a következő értékre: „%s”.',
  "Il contesto di database è stato sostituito con '%s'.",
  "データベース コンテキストが '%s' に変更されました。",
  "데이터베이스 컨텍스트가 '%s'(으)로 변경되었습니다.",
  'De databasecontext is gewijzigd in %s.',
  'Endret databasekontekst til %s.',
  'Zmieniono kontekst bazy danych na „%s”.',
  "Contexto do banco de dados alterado para '%s'.",
  'Контекст базы данных изменен на "%s".',
  'Ändrade databasens kontext till %s.',
  "Veritabanı bağlamı '%s' olarak değiştirildi.",
  '已将数据库上下文更改为 "%s"。',
  "O contexto da base de dados foi alterado para '%s'.",
  "Se cambió el contexto de la base de datos a '%s'.",
]

const escapeRegExp = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

const DATABASE_CONTEXT_LINE = new RegExp(
  `^(?:${DATABASE_CONTEXT_MESSAGES.map((m) => escapeRegExp(m).replace('%s', '.*')).join('|')})\\r?$`,
)

// Drops the first line of a mssql output only when it is the database context
// message; an error on the first line stays.
export function stripMssqlContext(output) {
  if (!output) return output
  const index = output.indexOf('\n')
  if (index === -1) return output
  return DATABASE_CONTEXT_LINE.test(output.slice(0, index)) ? output.slice(index + 1) : output
}

// The output as the viewer shows it.
export function sanitizeOutput(output, subtype) {
  return subtype === 'mssql' ? stripMssqlContext(output) : output
}

// ── Table ───────────────────────────────────────────────────────────────────

// The tab-separated output as { matrix, heads, body }. Oracle prints a line
// before its table. A final newline gives papaparse an empty last row, which is
// not data.
export function parseMatrix(output, subtype) {
  if (output == null) return null
  const input = subtype === 'oracledb' ? output.split('\n').slice(1).join('\n') : output
  const matrix = Papa.parse(input, { delimiter: '\t' }).data
  const last = matrix[matrix.length - 1]
  if (matrix.length > 1 && last?.length === 1 && last[0] === '') matrix.pop()
  return { matrix, heads: matrix[0] || [], body: matrix.slice(1) }
}

// A table needs a header and at least one row.
export const isTabular = (parsed) => Boolean(parsed && parsed.heads.length > 0 && parsed.body.length > 0)

const headerName = (header) => (typeof header === 'string' ? header : String(header))

// Columns are keyed by position: a header is free text, and ag-grid reads a
// field with a dot ("t.id") as a nested path and keeps one of two equal names.
export function columnDefs(headers) {
  return headers.map((header, i) => ({ field: `c${i}`, headerName: headerName(header) }))
}

// Normalizes each row to the header count, keyed like columnDefs. A value
// holding a tab widens its row: a wide row drops its empty cells first, so the
// rest lines up with the header again.
export function rowData(headers, rows) {
  const count = headers.length
  return rows.map((row) => {
    const source = row.length > count ? row.filter((cell) => cell != null && cell !== '') : row
    const entry = {}
    for (let i = 0; i < count; i += 1) {
      const cell = source[i]
      entry[`c${i}`] = cell == null ? '' : cell
    }
    return entry
  })
}

// ── Plain text ──────────────────────────────────────────────────────────────

// Row virtualization bounds height, not width: one unwrapped line of a few MB
// still hits Chrome's 2^25 px layout clamp, so a row is cut here and marked.
export const MAX_LINE_CHARS = 10 * 1024
export const LINE_HEIGHT = 20

export function humanizeSize(n) {
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${n} B`
}

export const isClipped = (line) => line.length > MAX_LINE_CHARS

// Splits on LF (and CRLF) and drops the trailing blank lines.
export function splitLines(output) {
  if (!output) return []
  const rows = output.split('\n').map((line) => (line.endsWith('\r') ? line.slice(0, -1) : line))
  while (rows.length && rows[rows.length - 1] === '') rows.pop()
  return rows
}

// Chrome clamps layout at 2^25 px. Above this the scroll box is scaled and the
// scroll range maps onto the row range, so the last row stays reachable.
export const MAX_SCROLL_HEIGHT = 16 * 1024 * 1024

// The rows to render for a scroll position. `end` is exclusive.
export function virtualWindow({ scrollTop, height, itemHeight, total, overscan }) {
  const realHeight = total * itemHeight
  const totalHeight = Math.min(realHeight, MAX_SCROLL_HEIGHT)
  const scaled = realHeight > MAX_SCROLL_HEIGHT
  const visible = Math.ceil(height / itemHeight)
  let firstRow
  if (scaled) {
    const range = Math.max(1, totalHeight - height)
    const maxStart = Math.max(0, total - visible)
    firstRow = Math.round((Math.min(scrollTop, range) / range) * maxStart)
  } else {
    firstRow = Math.floor(scrollTop / itemHeight)
  }
  const start = Math.max(0, Math.min(firstRow - overscan, Math.max(0, total - 1)))
  const end = Math.max(start, Math.min(total, firstRow + visible + overscan))
  let offsetY
  if (!scaled) offsetY = start * itemHeight
  // At the end of the range the last row sits on the bottom edge.
  else if (end === total) offsetY = scrollTop + height - (end - start) * itemHeight
  else offsetY = scrollTop - (firstRow - start) * itemHeight
  return { start, end, offsetY, totalHeight }
}

// ── Downloads ───────────────────────────────────────────────────────────────

const pad2 = (n) => String(n).padStart(2, '0')

// YYYYMMDD-HHMMSS in UTC: two users in different time zones get the same name.
export function filenameTimestamp(date = new Date()) {
  return (
    `${date.getUTCFullYear()}${pad2(date.getUTCMonth() + 1)}${pad2(date.getUTCDate())}-` +
    `${pad2(date.getUTCHours())}${pad2(date.getUTCMinutes())}${pad2(date.getUTCSeconds())}`
  )
}

export function buildFilename({ connectionName, sessionId }, ext, date) {
  const parts = [connectionName, sessionId].filter((part) => part && String(part).trim())
  parts.push(filenameTimestamp(date))
  return `${parts.join('-')}.${ext}`
}

export const matrixToCsv = (matrix) => Papa.unparse(matrix)

// An array of objects; a blank header becomes column_N (1-based).
export function matrixToJson(matrix) {
  const [heads = [], ...rows] = matrix
  const keys = heads.map((head, i) => (head == null || !String(head).trim() ? `column_${i + 1}` : head))
  const out = rows.map((row) => {
    const entry = {}
    const size = Math.min(keys.length, row.length)
    for (let i = 0; i < size; i += 1) entry[keys[i]] = row[i]
    return entry
  })
  return JSON.stringify(out, null, 2)
}

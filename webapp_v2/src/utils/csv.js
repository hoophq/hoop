// A spreadsheet runs a cell that starts with one of these as a formula.
// A leading quote makes it text (OWASP "CSV Injection").
const FORMULA_START = /^[=+\-@\t\r]/

function cell(value) {
  let text = value == null ? '' : String(value)
  if (FORMULA_START.test(text)) text = `'${text}`
  // RFC 4180: quote a field with a comma, a double quote or a line break.
  if (/[",\r\n]/.test(text)) text = `"${text.replace(/"/g, '""')}"`
  return text
}

// toCSV builds an RFC 4180 document: CRLF line ends and a UTF-8 BOM, so Excel
// reads accented text as UTF-8.
export function toCSV(header, rows) {
  const lines = [header, ...rows].map((row) => row.map(cell).join(','))
  return `\uFEFF${lines.join('\r\n')}\r\n`
}

export function downloadCSV(filename, text) {
  const blob = new Blob([text], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

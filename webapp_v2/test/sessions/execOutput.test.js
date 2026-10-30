import { describe, expect, it } from 'vitest'
import {
  MAX_LINE_CHARS,
  MAX_SCROLL_HEIGHT,
  buildFilename,
  columnDefs,
  decodeOutput,
  humanizeSize,
  isClipped,
  isTabular,
  matrixToJson,
  parseMatrix,
  rowData,
  sanitizeOutput,
  splitLines,
  stripMssqlContext,
  virtualWindow,
} from '@/features/SessionDetails/viewers/exec/output'

const b64 = (text) => btoa(String.fromCharCode(...new TextEncoder().encode(text)))

describe('decodeOutput', () => {
  it('decodes UTF-8 and turns the gateway tab marker back into a tab', () => {
    expect(decodeOutput(b64('id∞name\nção'))).toBe('id\tname\nção')
  })

  it('keeps the valid text around bytes that are not UTF-8', () => {
    expect(decodeOutput(btoa('ok\xff\xfeok'))).toBe('ok��ok')
  })

  it('shows a stream the gateway could not encode as its plain error text', () => {
    const error = 'An internal error occurred: unable to decode the stream'
    expect(decodeOutput(error)).toBe(error)
  })

  it('is empty without data', () => {
    expect(decodeOutput(undefined)).toBe('')
    expect(decodeOutput('')).toBe('')
  })
})

describe('stripMssqlContext (SUP-280)', () => {
  it('drops the database context line in any server language', () => {
    expect(stripMssqlContext("Changed database context to 'shop'.\nid\tname\n")).toBe('id\tname\n')
    expect(stripMssqlContext("Contexto do banco de dados alterado para 'x'.\r\nid\n")).toBe('id\n')
    expect(stripMssqlContext('Der Datenbankkontext wurde in "x" geändert.\nid')).toBe('id')
  })

  it('keeps an error on the first line', () => {
    const output = "Msg 208, Level 16: Invalid object name 'x'.\nmore"
    expect(stripMssqlContext(output)).toBe(output)
  })

  it('only applies to mssql', () => {
    const output = "Changed database context to 'shop'.\nid"
    expect(sanitizeOutput(output, 'postgres')).toBe(output)
    expect(sanitizeOutput(output, 'mssql')).toBe('id')
  })
})

// webapp/test/webapp/components/results_matrix_test.cljs
describe('parseMatrix', () => {
  it('parses heads and body without the empty row a final newline adds', () => {
    const parsed = parseMatrix('id\tname\n1\talice\n2\tbob\n')
    expect(parsed.heads).toEqual(['id', 'name'])
    expect(parsed.body).toEqual([['1', 'alice'], ['2', 'bob']])
    expect(isTabular(parsed)).toBe(true)
  })

  it('drops the line oracle prints before its table', () => {
    expect(parseMatrix('Session altered.\nID\n1', 'oracledb').heads).toEqual(['ID'])
  })

  it('is null without output and not tabular with a header only', () => {
    expect(parseMatrix(null)).toBeNull()
    expect(isTabular(parseMatrix('ERROR: relation "x" does not exist\n'))).toBe(false)
  })
})

// webapp/test/webapp/components/ag_grid_table_test.cljs
describe('columnDefs and rowData', () => {
  it('key columns by position and keep the header as the name, stringified', () => {
    expect(columnDefs(['id', 2])).toEqual([
      { field: 'c0', headerName: 'id' },
      { field: 'c1', headerName: '2' },
    ])
  })

  it('show a header with a dot and two equal headers as their own columns', () => {
    expect(columnDefs(['t.id', 'id', 'id']).map((c) => c.headerName)).toEqual(['t.id', 'id', 'id'])
    expect(rowData(['t.id', 'id', 'id'], [['1', '2', '3']])).toEqual([{ c0: '1', c1: '2', c2: '3' }])
  })

  it('key rows matching the header by column', () => {
    expect(rowData(['id', 'name'], [['1', 'alice'], ['2', 'bob']])).toEqual([
      { c0: '1', c1: 'alice' },
      { c0: '2', c1: 'bob' },
    ])
  })

  it('pad short rows and turn nil cells into empty strings', () => {
    expect(rowData(['a', 'b', 'c'], [['1', '2'], ['1']])).toEqual([
      { c0: '1', c1: '2', c2: '' },
      { c0: '1', c1: '', c2: '' },
    ])
    expect(rowData(['a', 'b'], [['1', null]])).toEqual([{ c0: '1', c1: '' }])
  })

  it('drop the empty cells of a wide row first', () => {
    expect(rowData(['a', 'b'], [['1', '2', '', '']])).toEqual([{ c0: '1', c1: '2' }])
    expect(rowData(['a', 'b'], [['', 'x', '', 'y']])).toEqual([{ c0: 'x', c1: 'y' }])
  })

  it('truncate a row that stays wide and pad one that gets short', () => {
    expect(rowData(['a', 'b'], [['1', '2', '3']])).toEqual([{ c0: '1', c1: '2' }])
    expect(rowData(['a', 'b', 'c'], [['1', '', '', '']])).toEqual([{ c0: '1', c1: '', c2: '' }])
  })

  it('produce no row data without rows', () => {
    expect(rowData(['a', 'b'], [])).toEqual([])
  })
})

// webapp/test/webapp/components/log_lines_test.cljs
describe('splitLines', () => {
  it('splits on LF and CRLF and drops trailing blank lines', () => {
    expect(splitLines('a\nb\r\nc\n\n')).toEqual(['a', 'b', 'c'])
  })

  it('has no rows for blank output', () => {
    expect(splitLines(null)).toEqual([])
    expect(splitLines('')).toEqual([])
  })

  it('clips a row above the line limit, not one at it', () => {
    expect(isClipped('x'.repeat(MAX_LINE_CHARS))).toBe(false)
    expect(isClipped('x'.repeat(MAX_LINE_CHARS + 1))).toBe(true)
  })

  it('humanizes sizes', () => {
    expect(humanizeSize(512)).toBe('512 B')
    expect(humanizeSize(1536)).toBe('1.5 KB')
    expect(humanizeSize(15 * 1024 * 1024)).toBe('15.0 MB')
  })
})

// webapp/test/webapp/components/virtualized_list_test.cljs
describe('virtualWindow', () => {
  const small = { height: 100, itemHeight: 20, total: 1000, overscan: 5 }
  const huge = { height: 100, itemHeight: 20, total: 2000000, overscan: 5 }

  it('keeps the rows of a small list at their pixel position', () => {
    expect(virtualWindow({ ...small, scrollTop: 0 })).toEqual({ start: 0, end: 10, offsetY: 0, totalHeight: 20000 })
    expect(virtualWindow({ ...small, scrollTop: 1000 })).toEqual({
      start: 45,
      end: 60,
      offsetY: 900,
      totalHeight: 20000,
    })
  })

  it('keeps the end of a small list within total', () => {
    const { start, end } = virtualWindow({ ...small, scrollTop: 19900 })
    expect([start, end]).toEqual([990, 1000])
  })

  it('caps the scroll box of a huge list and reaches its last row', () => {
    expect(virtualWindow({ ...huge, scrollTop: 0 }).totalHeight).toBe(MAX_SCROLL_HEIGHT)
    const { start, end, offsetY } = virtualWindow({ ...huge, scrollTop: MAX_SCROLL_HEIGHT - 100 })
    expect(end).toBe(2000000)
    expect(start).toBe(2000000 - 10)
    expect(offsetY).toBe(MAX_SCROLL_HEIGHT - 200)
  })

  it('ends the last row of a huge list on the viewport bottom', () => {
    const height = 354
    const scrollTop = MAX_SCROLL_HEIGHT - height
    const { start, end, offsetY } = virtualWindow({ ...huge, height, scrollTop })
    expect(end).toBe(2000000)
    expect(offsetY + (end - start) * 20).toBe(scrollTop + height)
  })

  it('renders the rows of a huge list at the viewport', () => {
    const { start, end, offsetY } = virtualWindow({ ...huge, scrollTop: 8000000 })
    expect(start).toBeGreaterThan(900000)
    expect(start).toBeLessThan(1100000)
    expect(end).toBe(start + 15)
    expect(offsetY).toBe(8000000 - 100)
  })

  it('handles an empty list', () => {
    expect(virtualWindow({ scrollTop: 0, height: 100, itemHeight: 20, total: 0, overscan: 5 })).toEqual({
      start: 0,
      end: 0,
      offsetY: 0,
      totalHeight: 0,
    })
  })
})

describe('downloads', () => {
  it('names the file after the connection, the session and the UTC time', () => {
    const date = new Date(Date.UTC(2026, 9, 3, 4, 5, 6))
    expect(buildFilename({ connectionName: 'seed-pg', sessionId: 'abc' }, 'csv', date)).toBe(
      'seed-pg-abc-20261003-040506.csv',
    )
    expect(buildFilename({ connectionName: ' ', sessionId: 'abc' }, 'txt', date)).toBe('abc-20261003-040506.txt')
  })

  it('writes JSON objects and names blank headers column_N', () => {
    expect(JSON.parse(matrixToJson([['id', ''], ['1', 'x'], ['2']]))).toEqual([
      { id: '1', column_2: 'x' },
      { id: '2' },
    ])
  })
})

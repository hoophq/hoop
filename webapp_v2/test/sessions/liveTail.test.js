import { describe, expect, it } from 'vitest'
import fixture from '../fixtures/sessions/session_live_tail.json'
import {
  deniedAt,
  deniedLabel,
  durationBetween,
  formatRelative,
  liveTerminalEvents,
  mergeStream,
  splitSse,
  streamEntry,
  streamRows,
} from '@/features/SessionDetails/decoders/liveTail'

describe('liveTerminalEvents matches the ClojureScript vectors', () => {
  it.each(fixture.cases)('$name', ({ recording_format: format, events, finalize, expected }) => {
    expect(liveTerminalEvents(format, events, finalize)).toEqual(expected)
  })
})

describe('splitSse', () => {
  it('reads events, skips comments and keeps a partial message', () => {
    const buffer =
      ': connected\n\nevent: event\ndata: {"type":"o"}\n\n: keepalive\n\nevent: session_end\ndata: {}\n\nevent: ev'
    const [messages, rest] = splitSse(buffer)
    expect(messages).toEqual([
      { event: 'event', data: '{"type":"o"}' },
      { event: 'session_end', data: '{}' },
    ])
    expect(rest).toBe('event: ev')
  })
  it('joins several data lines with a newline', () => {
    expect(splitSse('data: a\ndata: b\n\n')[0]).toEqual([{ event: 'message', data: 'a\nb' }])
  })
})

describe('streamEntry', () => {
  it('takes a sidecar elapsed time as is', () => {
    expect(streamEntry({ elapsed: 1.25, type: 'i', payload: 'eA==', time: '2026-10-03T00:00:09Z' }, '2026-10-03T00:00:00Z'))
      .toEqual([1.25, 'i', 'eA=='])
  })
  it('counts other events from the session start', () => {
    expect(streamEntry({ type: 'o', payload: 'eA==', time: '2026-10-03T00:00:02.5Z' }, '2026-10-03T00:00:00Z'))
      .toEqual([2.5, 'o', 'eA=='])
  })
})

describe('mergeStream', () => {
  it('fills the gap before the subscription and keeps each event once', () => {
    const history = [[1, 'o', 'YQ=='], [2.0004, 'o', 'Yg==']]
    const streamed = [[2, 'o', 'Yg=='], [3, 'o', 'Yw==']]
    expect(mergeStream(history, streamed)).toEqual([[1, 'o', 'YQ=='], [2.0004, 'o', 'Yg=='], [3, 'o', 'Yw==']])
  })
  it('keeps a key pressed twice in a row', () => {
    const history = [[1, 'i', 'bA==']]
    const streamed = [[1, 'i', 'bA=='], [1.2, 'i', 'bA==']]
    expect(mergeStream(history, streamed)).toEqual([[1, 'i', 'bA=='], [1.2, 'i', 'bA==']])
  })
})

describe('streamRows', () => {
  const q = (sql) => {
    const body = new TextEncoder().encode(`${sql}\0`)
    const bytes = new Uint8Array(5 + body.length)
    bytes[0] = 'Q'.charCodeAt(0)
    new DataView(bytes.buffer).setUint32(1, 4 + body.length)
    bytes.set(body, 5)
    return btoa(String.fromCharCode(...bytes))
  }

  it('turns a postgres Query frame into a query row', () => {
    const [row] = streamRows([[1, 'i', q('select 1')]], { postgres: true })
    expect(row).toMatchObject({ kind: 'query', pgTypeName: 'Query', sql: 'select 1' })
  })
  it('reads a raw-queries payload as one query', () => {
    const [row] = streamRows([[1, 'i', btoa('SELECT * FROM customers')]], { postgres: true })
    expect(row).toMatchObject({ kind: 'query', sql: 'SELECT * FROM customers' })
  })
  it('flags the denied statement, not the denial error at the same time', () => {
    const denied = deniedAt([{ elapsed: 2, rule_name: 'no-drop' }])
    const rows = streamRows([[2, 'i', btoa('drop table x')], [2, 'e', btoa('denied')]], { postgres: false, denied })
    expect(rows[0].deniedRules).toEqual(['no-drop'])
    expect(rows[1].deniedRules).toBeUndefined()
  })
})

describe('labels and times', () => {
  it('names the rules that denied', () => {
    expect(deniedLabel(['a', 'b', 'a'])).toBe('Denied by rules a, b')
    expect(deniedLabel(['a'])).toBe('Denied by rule a')
    expect(deniedLabel([''])).toBe('Denied by a guardrail')
  })
  it('formats offsets and durations', () => {
    expect(formatRelative(75.0421)).toBe('+01:15.042')
    expect(durationBetween('2026-10-03T00:00:00Z', '2026-10-03T01:02:05Z')).toBe('1h 2m')
    expect(durationBetween('2026-10-03T00:00:00Z', '2026-10-03T00:02:05Z')).toBe('2m 5s')
    expect(durationBetween('2026-10-03T00:00:00Z', null, Date.parse('2026-10-03T00:00:09Z'))).toBe('9s')
  })
})

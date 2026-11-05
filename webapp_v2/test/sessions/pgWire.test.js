import { describe, expect, it } from 'vitest'
import { isQueryFrame, parsePayload } from '@/features/SessionDetails/decoders/pgWire'

function frame(type, body) {
  const bytes = new Uint8Array(5 + body.length)
  bytes[0] = type.charCodeAt(0)
  new DataView(bytes.buffer).setUint32(1, 4 + body.length)
  bytes.set(body, 5)
  return bytes
}
const text = (s) => new TextEncoder().encode(s)
const b64 = (...frames) => {
  const all = frames.reduce((acc, f) => new Uint8Array([...acc, ...f]), new Uint8Array())
  return btoa(String.fromCharCode(...all))
}

describe('parsePayload', () => {
  it('reads Parse, Bind, Execute and Sync from one chunk', () => {
    const parse = frame('P', new Uint8Array([...text('s1\0select $1\0'), 0, 0]))
    const frames = parsePayload(b64(parse, frame('B', text('x')), frame('E', text('y')), frame('S', new Uint8Array())))
    expect(frames.map((f) => f.typeName)).toEqual(['Parse', 'Bind', 'Execute', 'Sync'])
    expect(frames[0].sql).toBe('select $1')
    expect(frames.filter(isQueryFrame)).toHaveLength(1)
  })
  it('drops a truncated tail', () => {
    const query = frame('Q', text('select 1\0'))
    const bytes = b64(query, frame('Q', text('select 2\0')).subarray(0, 6))
    expect(parsePayload(bytes).map((f) => f.sql)).toEqual(['select 1'])
  })
  it('reads a payload that is not base64 as no frames', () => {
    expect(parsePayload('%%%')).toEqual([])
  })
})

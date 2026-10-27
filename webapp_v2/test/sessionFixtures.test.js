import { describe, expect, it } from 'vitest'
import sessionFormat from './fixtures/sessions/session_format.json'
import sessionLiveTail from './fixtures/sessions/session_live_tail.json'
import sshDecoder from './fixtures/sessions/ssh_decoder.json'
import terminalDecoder from './fixtures/sessions/terminal_decoder.json'

// The fixtures are the ClojureScript session test vectors with the CLJS
// output (npm --prefix ../webapp run fixtures:sessions). The React decoders
// are tested against them, so this file holds their shape.

const STREAM_TYPES = ['i', 'o', 'e']
const DECODED_TYPES = ['i', 'o', 'e', 'r']

const fixtures = {
  'session_format.json': sessionFormat,
  'session_live_tail.json': sessionLiveTail,
  'ssh_decoder.json': sshDecoder,
  'terminal_decoder.json': terminalDecoder,
}

function expectStreamEvents(events) {
  expect(Array.isArray(events)).toBe(true)
  for (const event of events) {
    expect(event).toHaveLength(3)
    const [seconds, type, payload] = event
    expect(seconds).toBeTypeOf('number')
    expect(STREAM_TYPES).toContain(type)
    expect(payload).toBeTypeOf('string')
    // Canonical base64: decoding and encoding gives the same text.
    expect(btoa(atob(payload))).toBe(payload)
  }
}

function expectDecodedEvents(events) {
  expect(Array.isArray(events)).toBe(true)
  for (const event of events) {
    expect(event).toHaveLength(3)
    const [seconds, type, text] = event
    expect(seconds).toBeTypeOf('number')
    expect(DECODED_TYPES).toContain(type)
    expect(text).toBeTypeOf('string')
  }
}

describe.each(Object.entries(fixtures))('%s', (_file, fixture) => {
  it('names its source, generator and function', () => {
    expect(fixture.source).toMatch(/^webapp\/test\/.+\.cljs$/)
    expect(fixture.generator).toContain('fixtures:sessions')
    expect(fixture.function).toMatch(/^webapp\.audit\.views\.[a-z-]+\/[a-z-]+$/)
  })

  it('has cases with unique names', () => {
    expect(fixture.cases.length).toBeGreaterThan(0)
    const names = fixture.cases.map((c) => c.name)
    expect(new Set(names).size).toBe(names.length)
  })
})

describe('session_format.json', () => {
  it.each(sessionFormat.cases)('$name', (c) => {
    expect(c.session).toBeTypeOf('object')
    expect(['pty', 'ssh', 'rdp', 'raw']).toContain(c.expected)
  })
})

describe('terminal_decoder.json', () => {
  it.each(terminalDecoder.cases)('$name', (c) => {
    expectStreamEvents(c.events)
    expect(c.finalize).toBeTypeOf('boolean')
    expectDecodedEvents(c.expected)
  })
})

describe('ssh_decoder.json', () => {
  it.each(sshDecoder.cases)('$name', (c) => {
    expectStreamEvents(c.events)
    if (c.expected === null) return
    expect(Object.keys(c.expected).sort()).toEqual(['data', 'height', 'terminal', 'texts', 'width'])
    expect(c.expected.terminal).toBeTypeOf('boolean')
    expect(c.expected.data).toBeTypeOf('boolean')
    expect(c.expected.width).toBeTypeOf('number')
    expect(c.expected.height).toBeTypeOf('number')
    expectDecodedEvents(c.expected.texts)
  })
})

describe('session_live_tail.json', () => {
  it.each(sessionLiveTail.cases)('$name', (c) => {
    expect(['pty', 'ssh', 'raw']).toContain(c.recording_format)
    expectStreamEvents(c.events)
    expect(c.finalize).toBeTypeOf('boolean')
    if (c.expected !== null) expectDecodedEvents(c.expected)
  })
})

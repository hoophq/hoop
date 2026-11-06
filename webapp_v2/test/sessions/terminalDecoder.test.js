import { describe, expect, it } from 'vitest'
import fixture from '../fixtures/sessions/terminal_decoder.json'
import { b64ToText, decodeEvents, terminalText } from '@/features/SessionDetails/decoders/terminalDecoder'

describe('decodeEvents matches the ClojureScript vectors', () => {
  it.each(fixture.cases)('$name', ({ events, finalize, expected }) => {
    expect(decodeEvents(events, finalize)).toEqual(expected)
  })
})

describe('terminalText', () => {
  it('joins output and errors, not the echoed input', () => {
    expect(terminalText([[0, 'o', '$ '], [1, 'i', 'l'], [2, 'o', 'l'], [3, 'e', '!'], [4, 'r', '80x24']])).toBe('$ l!')
  })
})

describe('b64ToText', () => {
  it('decodes UTF-8', () => {
    expect(b64ToText(btoa('cafÃ©'))).toBe('café')
  })
  it('keeps an infinity sign (the ClojureScript decoder turned it into a tab)', () => {
    expect(b64ToText(btoa('â\u0088\u009e'))).toBe('∞')
  })
  it('shows bytes that are not UTF-8 as U+FFFD', () => {
    expect(b64ToText(btoa('ÿ'))).toBe('�')
  })
  it('reads a payload that is not base64 as no text', () => {
    expect(b64ToText('not base64!')).toBe('')
  })
})

import { describe, expect, it } from 'vitest'
import fixture from '../fixtures/sessions/ssh_decoder.json'
import { terminalRecording } from '@/features/SessionDetails/decoders/sshDecoder'
import { decodeByteEvents } from '@/features/SessionDetails/decoders/terminalDecoder'

// The vectors hold the recording's events with their bytes decoded.
function asFixture(recording) {
  if (!recording) return null
  return {
    terminal: recording.terminal,
    data: recording.data,
    width: recording.width,
    height: recording.height,
    texts: decodeByteEvents(recording.events),
  }
}

describe('terminalRecording matches the ClojureScript vectors', () => {
  it.each(fixture.cases)('$name', ({ events, expected }) => {
    expect(asFixture(terminalRecording(events))).toEqual(expected)
  })
})

import { describe, expect, it } from 'vitest'
import fixture from '../fixtures/sessions/session_format.json'
import { outputViewer, recordingFormat } from '@/features/SessionDetails/decoders/sessionFormat'

describe('recordingFormat matches the ClojureScript vectors', () => {
  it.each(fixture.cases)('$name', ({ session, expected }) => {
    expect(recordingFormat(session)).toBe(expected)
  })
})

describe('outputViewer', () => {
  it.each([
    [{ verb: 'exec', type: 'custom', connection_subtype: 'rdp' }, 'exec'],
    // P2: an open RDP session does not tail its bitmaps as text.
    [{ verb: 'connect', status: 'open', type: 'custom', connection_subtype: 'rdp' }, 'rdp'],
    [{ verb: 'connect', status: 'open', recording_format: 'rdp' }, 'rdp'],
    [{ verb: 'connect', status: 'open', type: 'custom', connection_subtype: 'bash' }, 'live'],
    [{ verb: 'connect', status: 'done', type: 'database', connection_subtype: 'postgres' }, 'live'],
    [{ verb: 'connect', status: 'done', type: 'custom', connection_subtype: 'rdp' }, 'rdp'],
    [{ verb: 'connect', status: 'done', recording_format: 'ssh' }, 'ssh'],
    [{ verb: 'connect', status: 'done', type: 'httpproxy' }, 'raw'],
  ])('%o is %s', (session, expected) => {
    expect(outputViewer(session)).toBe(expected)
  })
})

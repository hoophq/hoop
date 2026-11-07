import { describe, expect, it } from 'vitest'
import { streamRows } from '@/features/SessionDetails/decoders/liveTail'
import { mcpCatalogOutput } from '@/features/SessionDetails/decoders/sessionFormat'
import { maskingPanel } from '@/features/SessionDetails/insights/helpers'

const mcpSession = { identity_type: 'sidecar', connection_subtype: 'mcp' }
const stream = [
  [0.01, 'o', btoa('echo: Echo the message back.\n')],
  [1, 'i', btoa('echo\n{"message":"hi"}')],
]

describe('an mcp lane session', () => {
  it('reads its output as the tools/list catalog, and only a sidecar mcp session does', () => {
    expect(mcpCatalogOutput(mcpSession)).toBe(true)
    // An agent connection can carry the mcp subtype; its output is a response.
    expect(mcpCatalogOutput({ identity_type: 'user', connection_subtype: 'mcp' })).toBe(false)
    expect(mcpCatalogOutput({ identity_type: 'sidecar', connection_subtype: 'postgres' })).toBe(false)
  })

  it('makes the catalog a row of its own kind and keeps the call as text', () => {
    const rows = streamRows(stream, { mcp: true })
    expect(rows.map((row) => row.kind)).toEqual(['catalog', 'raw'])
    expect(rows[1].text).toBe('echo\n{"message":"hi"}')
    expect(streamRows(stream, {}).map((row) => row.kind)).toEqual(['output', 'raw'])
  })
})

describe('maskingPanel', () => {
  it('never offers a sidecar session to unlock masking', () => {
    expect(maskingPanel({ sidecar: true, hasData: false, presidio: false })).toBe('none')
    expect(maskingPanel({ sidecar: true, hasData: false, presidio: true })).toBe('none')
    expect(maskingPanel({ sidecar: true, hasData: true, presidio: false })).toBe('report')
  })

  it('keeps the agent behaviour', () => {
    expect(maskingPanel({ sidecar: false, hasData: false, presidio: false })).toBe('unlock')
    expect(maskingPanel({ sidecar: false, hasData: false, presidio: true })).toBe('report')
    expect(maskingPanel({ sidecar: false, hasData: true, presidio: false })).toBe('report')
  })
})

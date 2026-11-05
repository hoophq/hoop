import { describe, expect, it } from 'vitest'
import { protocolInfo } from '@/pages/Sidecars/config'
import { formToListener, listenerToForm } from '@/pages/Sidecars/listeners'
import { listenerAccepts } from '@/pages/Sidecars/schema'
import { operationsFor, ruleTypesFor } from '@/pages/sidecarRuleVocabulary'

const types = (protocols) => ruleTypesFor(protocols).map((t) => t.value)

describe('an mcp lane', () => {
  it('has an icon', () => {
    expect(protocolInfo('mcp').subtype).toBe('mcp')
  })

  it('has its own operations', () => {
    expect(operationsFor('mcp')).toEqual(['call', 'show'])
  })

  it('is the only lane offered mcp_tool', () => {
    expect(types(['mcp'])).toContain('mcp_tool')
    expect(types(['postgres'])).not.toContain('mcp_tool')
    expect(types(['mcp', 'postgres'])).not.toContain('mcp_tool')
  })

  it('renders its mcp_gateway, caller_token and downstream_tls blocks', () => {
    for (const key of ['mcp_gateway', 'caller_token', 'downstream_tls']) {
      expect(listenerAccepts('mcp', key)).toBe(true)
    }
    expect(listenerAccepts('postgres', 'mcp_gateway')).toBe(false)
  })

  it('keeps the mcp_gateway keys the form does not render', () => {
    const form = listenerToForm({
      name: 'tools',
      protocol: 'mcp',
      listen: '127.0.0.1:18901',
      upstream: 'http://127.0.0.1:18900/mcp',
      mcp_gateway: { transport: 'streamable-http', max_calls_per_session: 5, tool_rate_limits: { 'search_*': 30 } },
    })
    expect(formToListener(form).mcp_gateway).toEqual({ max_calls_per_session: 5, tool_rate_limits: { 'search_*': 30 } })
  })
})

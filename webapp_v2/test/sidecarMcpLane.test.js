import { describe, expect, it, vi } from 'vitest'
import { laneFeatureBlocked, protocolInfo } from '@/pages/Sidecars/config'
import { featureList } from '@/pages/Sidecars/features'
import { formToListener, listenerToForm, protocolOptions } from '@/pages/Sidecars/listeners'
import { addBlocked, targetLanes } from '@/pages/Sidecars/rules'
import { listenerAccepts } from '@/pages/Sidecars/schema'
import { GUARDRAIL_RULE_TYPES, operationsFor, ruleTypesFor } from '@/pages/sidecarRuleVocabulary'

// rules.js names the rule services; these tests call none of them.
vi.mock('@/services/aiSessionAnalyzer', () => ({ aiSessionAnalyzerService: {} }))
vi.mock('@/services/dataMasking', () => ({ dataMaskingService: {} }))
vi.mock('@/services/guardrails', () => ({ guardrailsService: {} }))

const types = (protocols) => ruleTypesFor(protocols).map((t) => t.value)
const offersProtocol = (protocol, current, capabilities) =>
  protocolOptions(current, capabilities).some((o) => o.value === protocol)
const offers = (current, capabilities) => offersProtocol('mcp', current, capabilities)

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

  it('is offered only to a sidecar that reports protocol:mcp, or that never handshaked', () => {
    expect(offers('', ['protocol:mcp', 'protocol:postgres'])).toBe(true)
    expect(offers('', ['protocol:postgres'])).toBe(false)
    expect(offers('', [])).toBe(false)
    expect(offers('', null)).toBe(true)
    // A lane already on mcp keeps its protocol in the picker.
    expect(offers('mcp', [])).toBe(true)
  })

  it('gates ica the same way', () => {
    expect(offersProtocol('ica', '', ['protocol:ica'])).toBe(true)
    expect(offersProtocol('ica', '', ['protocol:mcp', 'protocol:postgres'])).toBe(false)
    expect(offersProtocol('ica', '', null)).toBe(true)
    expect(offersProtocol('ica', 'ica', [])).toBe(true)
    expect(offersProtocol('postgres', '', [])).toBe(true)
  })

  it('scopes an mcp_tool rule to call, the one operation it matches', () => {
    expect(GUARDRAIL_RULE_TYPES.find((t) => t.value === 'mcp_tool').scope).toEqual(['call'])
  })

  it('names the tools of a listener mcp_tool rule', () => {
    const detail = (rule) => {
      const listener = { name: 'tools', protocol: 'mcp', guardrails: { rules: [{ name: 'r', type: 'mcp_tool', ...rule }] } }
      const guardrails = featureList(listener, { listeners: [listener] }, []).find((f) => f.key === 'guardrails')
      return guardrails.rules[0].detail
    }
    expect(detail({ tools: ['*delete*'] })).toBe('MCP tool · deny *delete*')
    expect(detail({ allow: ['get_*', 'list_*'] })).toBe('MCP tool · allow only get_*, list_*')
  })

  it('takes mask rules but no analyzer rule, and a mixed sidecar binds the analyzer to its other lanes', () => {
    expect(laneFeatureBlocked('mcp', 'data-masking')).toBeNull()
    expect(laneFeatureBlocked('mcp', 'ai-analyzer')).toBeTruthy()
    expect(laneFeatureBlocked('mcp', 'guardrails')).toBeNull()
    const mcp = { name: 'tools', protocol: 'mcp' }
    const pg = { name: 'appdb', protocol: 'postgres' }
    const sidecar = { configuration: { listeners: [mcp, pg], analyzer: { provider: 'anthropic' } } }
    expect(addBlocked(sidecar, mcp, 'data-masking')).toBeNull()
    expect(addBlocked(sidecar, mcp, 'ai-analyzer')).toBe(laneFeatureBlocked('mcp', 'ai-analyzer'))
    expect(addBlocked(sidecar, mcp, 'guardrails')).toBeNull()
    expect(targetLanes(sidecar, null, 'data-masking')).toEqual(['tools', 'appdb'])
    expect(targetLanes(sidecar, null, 'ai-analyzer')).toEqual(['appdb'])
    expect(targetLanes(sidecar, null, 'guardrails')).toEqual(['tools', 'appdb'])
  })
})

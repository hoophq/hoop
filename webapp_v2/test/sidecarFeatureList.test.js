import { describe, expect, it } from 'vitest'
import { featureList } from '@/pages/Sidecars/features'

// A document Hoop owns, stored as sent: the rules sit on the listeners.
const config = {
  mask: { rules: [{ name: 'pii-redact', entities: ['EMAIL_ADDRESS'], strategy: 'redact' }] },
  analyzer: { provider: 'gemini', model: 'gemini-3.8-flash' },
  listeners: [
    {
      name: 'appdb',
      protocol: 'postgres',
      listen: '0.0.0.0:15432',
      upstream: 'db:5432',
      analyzer: { trigger: { operations: ['update', 'delete'] }, high: 'block' },
    },
    {
      name: 'httpbin',
      protocol: 'http',
      listen: '0.0.0.0:18080',
      upstream: 'httpbin:8080',
      guardrails: {
        mode: 'enforce',
        rules: [{ name: 'no-admin-api', type: 'http_resource', resources: ['/anything/admin/**'] }],
      },
    },
  ],
}

const card = (key, boundRules = []) => featureList(null, config, boundRules).find((f) => f.key === key)

describe('the sidecar-level feature card', () => {
  it("lists a listener's own analyzer block", () => {
    const analyzer = card('ai-analyzer')
    expect(analyzer.summary).toBe('1 rule')
    expect(analyzer.rules).toMatchObject([{ name: 'Analyzer', detail: 'On appdb', source: 'listener' }])
  })

  it("lists a listener's own guardrail", () => {
    const guardrails = card('guardrails')
    expect(guardrails.summary).toBe('1 rule')
    expect(guardrails.rules).toMatchObject([{ name: 'no-admin-api', detail: 'On httpbin', source: 'listener' }])
  })

  it('counts an inherited rule once, as the sidecar one', () => {
    const masking = card('data-masking')
    expect(masking.summary).toBe('1 rule')
    expect(masking.rules).toMatchObject([{ name: 'pii-redact', source: 'sidecar' }])
  })

  it("leaves the lanes' own rules out of a new listener's defaults", () => {
    const defaults = featureList(null, config, [], { defaultsOnly: true })
    const byKey = Object.fromEntries(defaults.map((f) => [f.key, f]))
    expect(byKey['ai-analyzer'].summary).toBe('No rule configured')
    expect(byKey.guardrails.summary).toBe('No rule configured')
    expect(byKey['data-masking'].rules.map((r) => r.name)).toEqual(['pii-redact'])
  })

  it("marks a lane rule whose lane overrides the card's guardrail mode", () => {
    const withModes = (top, lane) => ({
      ...config,
      guardrails: { mode: top },
      listeners: config.listeners.map((l) =>
        l.guardrails ? { ...l, guardrails: { ...l.guardrails, mode: lane } } : l,
      ),
    })
    const guardrails = (cfg) => featureList(null, cfg, []).find((f) => f.key === 'guardrails')

    const enforcingLane = guardrails(withModes('observe', 'enforce'))
    expect(enforcingLane.mode).toBe('observe')
    expect(enforcingLane.rules[0].mode).toBe('enforce')

    const observingLane = guardrails(withModes('enforce', 'observe'))
    expect(observingLane.mode).toBe('enforce')
    expect(observingLane.rules[0].mode).toBe('observe')

    expect(guardrails(withModes('observe', 'observe')).rules[0].mode).toBeNull()
  })

  it('lists a distributed rule next to the inline one', () => {
    const bound = [{ kind: 'guardrail', rule_name: 'no-drop', listener_name: 'appdb' }]
    expect(card('guardrails', bound).rules.map((r) => r.name)).toEqual(['no-drop', 'no-admin-api'])
  })
})

describe('the approver of a held level in the analyzer summary', () => {
  const detail = (approver) => {
    const listener = { name: 'appdb', analyzer: { high: 'require_approval', approvers: { high: approver } } }
    return featureList(listener, { listeners: [listener] }, []).find((f) => f.key === 'ai-analyzer').rules[0].detail
  }

  it('names the groups, and the delivery when notify is set', () => {
    expect(detail({ type: 'human', groups: ['dba'] })).toBe('Every statement · high: hold (Human: dba)')
    expect(detail({ type: 'human', groups: ['dba'], notify: { slack: { delivery: 'dm' } } })).toBe(
      'Every statement · high: hold (Human: dba by Slack direct message)',
    )
    expect(detail({ type: 'human', notify: { slack: {} } })).toBe(
      'Every statement · high: hold (Human: administrators by Slack direct message)',
    )
    expect(
      detail({ type: 'human', groups: ['dba', 'sre'], notify: { slack: { delivery: 'both' }, teams: { delivery: 'channel' } } }),
    ).toBe('Every statement · high: hold (Human: dba, sre by Slack direct message and channel and Teams channel)')
  })

  it('keeps the summary of a routed level', () => {
    expect(detail({ type: 'human', routing: { candidates: ['dba'], notify: { slack: {} } } })).toBe(
      'Every statement · high: hold (Human: AI routing from dba)',
    )
  })
})

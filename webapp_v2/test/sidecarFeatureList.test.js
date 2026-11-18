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

  it('lists a distributed rule next to the inline one', () => {
    const bound = [{ kind: 'guardrail', rule_name: 'no-drop', listener_name: 'appdb' }]
    expect(card('guardrails', bound).rules.map((r) => r.name)).toEqual(['no-drop', 'no-admin-api'])
  })
})

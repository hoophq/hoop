import { describe, expect, it } from 'vitest'
import { accessRequestOverlap } from '@/pages/Features/AiSessionAnalyzer/helpers'

const rule = (name, access_type, connection_names = [], attributes = []) => ({
  name,
  access_type,
  connection_names,
  attributes,
})

describe('accessRequestOverlap', () => {
  it('flags a role a command rule names', () => {
    const rules = [rule('ar-cmd', 'command', ['pg-prod'])]
    expect(accessRequestOverlap(['pg-prod', 'pg-dev'], rules, [])).toEqual([
      { connectionName: 'pg-prod', rules: [rules[0]] },
    ])
  })

  it('flags a jit_command rule', () => {
    const rules = [rule('ar-both', 'jit_command', ['pg-prod'])]
    expect(accessRequestOverlap(['pg-prod'], rules, [])).toHaveLength(1)
  })

  it('ignores a jit-only rule', () => {
    const rules = [rule('ar-jit', 'jit', ['pg-prod'])]
    expect(accessRequestOverlap(['pg-prod'], rules, [])).toEqual([])
  })

  it('ignores a sidecar rule', () => {
    const rules = [rule('ar-sidecar', 'sidecar', ['pg-prod'])]
    expect(accessRequestOverlap(['pg-prod'], rules, [])).toEqual([])
  })

  it('flags a role that shares an attribute with a command rule', () => {
    const rules = [rule('ar-attr', 'command', [], ['production'])]
    const attributes = [{ name: 'production', connection_names: ['pg-prod'] }]
    expect(accessRequestOverlap(['pg-prod', 'pg-dev'], rules, attributes)).toEqual([
      { connectionName: 'pg-prod', rules: [rules[0]] },
    ])
  })

  it('does not flag an attribute the role does not have', () => {
    const rules = [rule('ar-attr', 'command', [], ['production'])]
    const attributes = [{ name: 'production', connection_names: ['pg-other'] }]
    expect(accessRequestOverlap(['pg-prod'], rules, attributes)).toEqual([])
  })

  it('lists every covering rule once, sorted by name', () => {
    const rules = [
      rule('zeta', 'command', ['pg-prod'], ['production']),
      rule('alpha', 'command', ['pg-prod']),
    ]
    const attributes = [{ name: 'production', connection_names: ['pg-prod'] }]
    const [entry] = accessRequestOverlap(['pg-prod'], rules, attributes)
    expect(entry.rules.map((r) => r.name)).toEqual(['alpha', 'zeta'])
  })

  it('returns nothing without a selection or without rules', () => {
    expect(accessRequestOverlap([], [rule('a', 'command', ['x'])], [])).toEqual([])
    expect(accessRequestOverlap(['x'], [], [])).toEqual([])
    expect(accessRequestOverlap(['x'], undefined, undefined)).toEqual([])
  })
})

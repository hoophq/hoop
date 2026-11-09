import { describe, expect, it } from 'vitest'
import { readLevelHandoff, writeLevelHandoff } from '@/pages/sidecarRuleVocabulary'

const ALL_FOUR = ['allow', 'warn', 'block', 'require_approval']

describe('a hand-off level', () => {
  it('allows all four when it has no handoffs entry', () => {
    expect(readLevelHandoff({ high: 'ai-handoff' }, 'high')).toEqual({ allowed: ALL_FOUR, prompt: '' })
  })

  it('allows all four when its entry has no allowed_actions', () => {
    const spec = { high: 'ai-handoff', handoffs: { high: { prompt: 'Prefer warn' } } }
    expect(readLevelHandoff(spec, 'high')).toEqual({ allowed: ALL_FOUR, prompt: 'Prefer warn' })
  })

  it('reads the listed actions, the hold in either spelling', () => {
    const spec = { high: 'ai-handoff', handoffs: { high: { allowed_actions: ['require_review', 'warn'] } } }
    expect(readLevelHandoff(spec, 'high').allowed).toEqual(['warn', 'require_approval'])
  })

  it('saves the explicit list, so the spec never relies on absence', () => {
    expect(writeLevelHandoff({ allowed: ALL_FOUR, prompt: ' ' })).toEqual({ allowed_actions: ALL_FOUR })
    expect(writeLevelHandoff({ allowed: ['warn'], prompt: 'Prefer warn' })).toEqual({
      allowed_actions: ['warn'],
      prompt: 'Prefer warn',
    })
  })
})

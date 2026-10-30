import { describe, expect, it } from 'vitest'
import { blockReasonTab, insightsByTab, isOutputHidden, sessionStatusKey } from '@/features/SessionDetails/sessionState'

describe('sessionStatusKey', () => {
  it.each([
    [{ status: 'done', verb: 'exec', exit_code: 0 }, 'done'],
    [{ status: 'done', verb: 'exec', exit_code: 1 }, 'failed'],
    [{ status: 'done', verb: 'connect', exit_code: 1 }, 'done'],
    [{ status: 'done', verb: 'exec', exit_code: 1, guardrails_info: [{}] }, 'blocked'],
    [{ status: 'done', verb: 'exec', exit_code: 1, ai_analysis: { action: 'block_execution' } }, 'blocked'],
    [{ status: 'done', verb: 'exec', review: { status: 'REJECTED' } }, 'rejected'],
    [{ status: 'ready', verb: 'exec' }, 'ready'],
    [{ status: 'open', verb: 'exec', exit_code: 1 }, 'open'],
  ])('%o is %s', (session, expected) => {
    expect(sessionStatusKey(session)).toBe(expected)
  })
})

describe('isOutputHidden', () => {
  const pending = { id: 'r', status: 'PENDING', review_groups_data: [{ status: 'PENDING', group: 'g' }] }
  it.each([
    ['a pending review', { verb: 'exec', status: 'open', review: pending }, undefined, true],
    ['a ready exec', { verb: 'exec', status: 'ready' }, undefined, true],
    ['credentials', { verb: 'connect', status: 'open', metadata: { credentials_expire_at: 'x' } }, undefined, true],
    ['a reviewed exec running', { verb: 'exec', status: 'ready', review: { id: 'r', status: 'APPROVED' } }, { status: 'running' }, true],
    ['a finished exec', { verb: 'exec', status: 'done', review: { id: 'r', status: 'APPROVED' } }, undefined, false],
  ])('%s', (_name, session, execution, expected) => {
    expect(isOutputHidden(session, execution)).toBe(expected)
  })
})

describe('insights by tab (NB19)', () => {
  const block = { action: 'block_execution', risk_level: 'high' }
  const input = { direction: 'input', rule: { type: 'deny_words_list' } }
  const legacy = { rule: { type: 'deny_words_list' } }
  const output = { direction: 'output', rule: { type: 'pattern_match' } }

  it('puts the analyzer and the input rules in Input, the output rules in Output', () => {
    expect(insightsByTab({ ai_analysis: block, guardrails_info: [input, legacy, output] })).toEqual({
      input: { analysis: block, guardrails: [input, legacy] },
      output: { guardrails: [output] },
    })
    expect(insightsByTab({ ai_analysis: {}, guardrails_info: null })).toEqual({
      input: { analysis: null, guardrails: [] },
      output: { guardrails: [] },
    })
  })

  it('opens a blocked session on the tab that holds the reason', () => {
    expect(blockReasonTab({ ai_analysis: block })).toBe('input')
    expect(blockReasonTab({ guardrails_info: [legacy] })).toBe('input')
    expect(blockReasonTab({ guardrails_info: [output] })).toBe('output')
    expect(blockReasonTab({ guardrails_info: [output, input] })).toBe('input')
    expect(blockReasonTab({ ai_analysis: { action: 'allow_execution' } })).toBe(null)
    expect(blockReasonTab({})).toBe(null)
  })
})

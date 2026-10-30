import { describe, expect, it } from 'vitest'
import {
  blockReasonTab,
  canDownloadInput,
  executedBy,
  insightsByTab,
  isOutputHidden,
  reviewBadgeStatus,
  sessionStatusKey,
} from '@/features/SessionDetails/sessionState'

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

describe('executed by (NB12)', () => {
  const review = (status, executor) => ({ status, executed_by: executor })
  it('names who ran the review when it was not the creator', () => {
    expect(executedBy({ user: 'owner@x.com', review: review('EXECUTED', 'admin@x.com') })).toBe('admin@x.com')
    expect(executedBy({ user: 'owner@x.com', review: review('PROCESSING', 'admin@x.com') })).toBe('admin@x.com')
  })
  it('hides the row for the creator, an older review and a reset run', () => {
    expect(executedBy({ user: 'owner@x.com', review: review('EXECUTED', 'owner@x.com') })).toBe(null)
    expect(executedBy({ user: 'owner@x.com', review: review('EXECUTED', undefined) })).toBe(null)
    expect(executedBy({ user: 'owner@x.com', review: review('APPROVED', 'admin@x.com') })).toBe(null)
    expect(executedBy({ user: 'owner@x.com' })).toBe(null)
  })
})

describe('review badge (E1)', () => {
  it.each([
    ['APPROVED', 'APPROVED'],
    ['PROCESSING', 'APPROVED'],
    ['EXECUTED', 'APPROVED'],
    ['PENDING', 'PENDING'],
    ['REJECTED', 'REJECTED'],
    ['REVOKED', null],
    ['EXPIRED', null],
    [undefined, null],
  ])('%s draws %s', (status, expected) => {
    expect(reviewBadgeStatus(status)).toBe(expected)
  })
})

describe('input download (NB01)', () => {
  const session = { user_id: 'owner-1' }
  it('shows to the owner, an admin and an auditor', () => {
    expect(canDownloadInput(session, { user: { id: 'owner-1' }, role: 'standard' })).toBe(true)
    expect(canDownloadInput(session, { user: { id: 'admin-1' }, role: 'admin' })).toBe(true)
    expect(canDownloadInput(session, { user: { id: 'auditor-1' }, role: 'auditor' })).toBe(true)
  })
  it('hides from an approver the gateway refuses', () => {
    expect(canDownloadInput(session, { user: { id: 'approver-1' }, role: 'approver' })).toBe(false)
    expect(canDownloadInput({ user_id: '' }, { user: { id: '' }, role: 'standard' })).toBe(false)
  })
})

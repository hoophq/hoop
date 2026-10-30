import { describe, expect, it } from 'vitest'
import { isOutputHidden, sessionStatusKey } from '@/features/SessionDetails/sessionState'

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

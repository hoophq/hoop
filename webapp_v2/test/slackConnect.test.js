import { describe, expect, it } from 'vitest'
import { canConnectSlack, promptDismissKey, shouldShowPrompt } from '@/features/SlackConnect/helpers'

const user = { id: 'u1', slack_id: '' }
const open = { available: true, linked: false }
const base = { enabled: true, user, status: open, dismissedKey: null }

describe('promptDismissKey', () => {
  it('is per user', () => {
    expect(promptDismissKey('u1')).toBe('slack-connect-dismissed-for:u1')
    expect(promptDismissKey('u1')).not.toBe(promptDismissKey('u2'))
  })

  it('is null without a user', () => {
    expect(promptDismissKey(undefined)).toBeNull()
  })
})

describe('shouldShowPrompt', () => {
  it('shows with the flag on, no Slack user, Slack available and no dismissal', () => {
    expect(shouldShowPrompt(base)).toBe(true)
  })

  it.each([
    ['the flag is off', { enabled: false }],
    ['the user is not loaded', { user: null }],
    ['the user has a Slack user', { user: { id: 'u1', slack_id: 'U0123' } }],
    ['the status is not loaded', { status: null }],
    ['Slack is not available', { status: { available: false, linked: false } }],
    ['the gateway says linked', { status: { available: true, linked: true } }],
    ['the user said "Not now"', { dismissedKey: promptDismissKey('u1') }],
  ])('hides when %s', (_, patch) => {
    expect(shouldShowPrompt({ ...base, ...patch })).toBe(false)
  })

  it('shows when another user dismissed it', () => {
    expect(shouldShowPrompt({ ...base, dismissedKey: promptDismissKey('u2') })).toBe(true)
  })
})

describe('canConnectSlack', () => {
  it('ignores the dismissal: the menu item stays', () => {
    expect(canConnectSlack(base)).toBe(true)
  })

  it.each([
    ['the flag is off', { enabled: false }],
    ['the user has a Slack user', { user: { id: 'u1', slack_id: 'U0123' } }],
    ['Slack is not available', { status: { available: false, linked: false } }],
  ])('is false when %s', (_, patch) => {
    expect(canConnectSlack({ ...base, ...patch })).toBe(false)
  })
})

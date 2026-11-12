import { describe, expect, it } from 'vitest'
import { notifyFallbackLabel } from '@/pages/Reviews/helpers'

describe('the notification fallback of a routed review', () => {
  it('names each known cause and says the review was posted as without routing', () => {
    expect(notifyFallbackLabel('no_integration')).toBe('No notify integration is set up. Posted as without routing.')
    expect(notifyFallbackLabel('no_target')).toBe(
      'The AI agent selected nobody it could reach. Posted as without routing.',
    )
    expect(notifyFallbackLabel('not_delivered')).toBe('Every routed notification failed. Posted as without routing.')
  })

  it('shows a cause this build does not know as sent', () => {
    expect(notifyFallbackLabel('new_cause')).toBe(
      'The routed notifications reached nobody (new_cause). Posted as without routing.',
    )
  })
})

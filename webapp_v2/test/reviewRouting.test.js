import { describe, expect, it } from 'vitest'
import { notifyFallbackLabel } from '@/pages/Reviews/helpers'

describe('the notification fallback of a routed review', () => {
  it('names each known cause and says the post as without routing reached a channel', () => {
    expect(notifyFallbackLabel('no_integration', true)).toBe(
      'No notify integration is set up. Posted as without routing.',
    )
    expect(notifyFallbackLabel('no_target', true)).toBe(
      'The AI agent selected nobody it could reach. Posted as without routing.',
    )
    expect(notifyFallbackLabel('not_delivered', true)).toBe(
      'Every routed notification failed. Posted as without routing.',
    )
  })

  it('never claims a post that no channel accepted', () => {
    expect(notifyFallbackLabel('no_integration', false)).toBe(
      'No notify integration is set up. The post as without routing reached nobody either.',
    )
    expect(notifyFallbackLabel('no_target')).toBe(
      'The AI agent selected nobody it could reach. The post as without routing reached nobody either.',
    )
  })

  it('shows a cause this build does not know as sent', () => {
    expect(notifyFallbackLabel('new_cause', true)).toBe(
      'The routed notifications reached nobody (new_cause). Posted as without routing.',
    )
  })
})

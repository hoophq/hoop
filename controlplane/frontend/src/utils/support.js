import { useUserStore } from '@/stores/useUserStore'

export const GITHUB_DISCUSSIONS_URL = 'https://github.com/hoophq/hoop/discussions'
export const SALES_URL = 'https://hoop.dev/meet'
export const UPGRADE_MESSAGE = 'I want to upgrade my current plan'

// Same destination as "Contact support" in the header user menu.
export function openSupport(message) {
  if (useUserStore.getState().showIntercomMessage(message)) return
  window.open(GITHUB_DISCUSSIONS_URL, '_blank', 'noopener,noreferrer')
}

// Every "Talk to sales" action: Intercom when analytics tracking allows it,
// otherwise the meeting page in a new tab.
export function openSales(message = UPGRADE_MESSAGE) {
  if (useUserStore.getState().showIntercomMessage(message)) return
  window.open(SALES_URL, '_blank', 'noopener,noreferrer')
}

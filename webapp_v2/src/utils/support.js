import { useUserStore } from '@/stores/useUserStore'

// Fallback for when Intercom is unavailable: analytics off, or the widget
// failed to load.
export const GITHUB_DISCUSSIONS_URL = 'https://github.com/hoophq/hoop/discussions'

// Opens the Intercom messenger with a prefilled message, same destination as
// "Contact support" in the header user menu. The store boots it when needed.
export function openSupport(message) {
  if (useUserStore.getState().showIntercomMessage(message)) return
  window.open(GITHUB_DISCUSSIONS_URL, '_blank', 'noopener,noreferrer')
}

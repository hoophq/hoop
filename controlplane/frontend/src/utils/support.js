import { useUserStore } from '@/stores/useUserStore'

export const GITHUB_DISCUSSIONS_URL = 'https://github.com/hoophq/hoop/discussions'

// Same destination as "Contact support" in the header user menu.
export function openSupport(message) {
  if (useUserStore.getState().showIntercomMessage(message)) return
  window.open(GITHUB_DISCUSSIONS_URL, '_blank', 'noopener,noreferrer')
}

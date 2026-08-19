// Fallback for when analytics is off: Intercom is never booted in that case.
export const GITHUB_DISCUSSIONS_URL = 'https://github.com/hoophq/hoop/discussions'

// Opens the Intercom messenger with a prefilled message, same destination as
// "Contact support" in the header user menu.
export function openSupport(analyticsTracking, message) {
  if (analyticsTracking && window.Intercom) {
    window.Intercom('showNewMessage', message)
    return
  }
  window.open(GITHUB_DISCUSSIONS_URL, '_blank', 'noopener,noreferrer')
}

export const SLACK_CONNECT_FLAG = 'experimental.slack_connect_code'

// How often the modal asks the gateway while a code is out: the live event is best effort.
export const LINK_POLL_MS = 10000

// "Not now" on the prompt, kept per user in this browser.
const DISMISS_KEY = 'slack-connect-dismissed-for'

export const promptDismissKey = (userId) => (userId ? `${DISMISS_KEY}:${userId}` : null)

// True when this user can link a Slack user now: the user-menu item follows it.
// `status` is the answer of GET /users/self/slack/link, null until it arrives.
export function canConnectSlack({ enabled, user, status }) {
  if (!enabled || !user?.id) return false
  if (user.slack_id || status?.linked) return false
  return status?.available === true
}

// The prompt also needs no "Not now" from this user. `dismissedKey` is the
// dismiss key the browser holds for the user, null when it holds none.
export function shouldShowPrompt({ enabled, user, status, dismissedKey }) {
  if (!canConnectSlack({ enabled, user, status })) return false
  return dismissedKey !== promptDismissKey(user.id)
}

// "10:10 AM" in the browser's time zone.
export function formatExpiry(expiresAt) {
  const date = new Date(expiresAt)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' })
}

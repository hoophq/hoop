/**
 * Pure helpers shared by the Sessions surfaces. They live outside the component
 * files so those stay component-only (react-refresh).
 */

/** A session is live when it is a `connect` still open (session_item.cljs:71-73). */
export function isLiveSession(session) {
  return session?.verb === 'connect' && session?.status === 'open'
}

/**
 * v1 indexed straight into `user_name`, so a machine-identity session with a
 * null name threw and took the whole list down. Fall back to the email, then to
 * a placeholder.
 */
export function displayNameFor(session) {
  return session?.user_name || session?.user || 'Unknown'
}

/** First letter of the first and last name tokens, as `user-icon/initials-black` did. */
export function initialsFor(name) {
  const tokens = String(name ?? '')
    .trim()
    .split(/\s+/)
    .filter(Boolean)
  if (!tokens.length) return '?'
  const first = tokens[0][0] ?? ''
  const last = tokens.length > 1 ? (tokens[tokens.length - 1][0] ?? '') : ''
  return (first + last).toUpperCase()
}

// The owners of a sidecar, as the form holds them: a list of emails.
// The gateway applies the same rules (PUT /sidecars/:nameOrId/owners) and
// answers 422; these keep a typo from making the trip.
export const MAX_OWNERS = 20

const EMAIL_RE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/

/** Trimmed, lowercased, no blanks, no repeats: what the gateway would store. */
export function normalizeOwners(entries) {
  const seen = new Set()
  for (const entry of entries ?? []) {
    const email = String(entry ?? '')
      .trim()
      .toLowerCase()
    if (email) seen.add(email)
  }
  return [...seen]
}

/** The field error for a normalized list, or null when it can be saved. */
export function ownersFieldError(owners) {
  const invalid = owners.filter((email) => !EMAIL_RE.test(email))
  if (invalid.length === 1) return `"${invalid[0]}" is not an email. Type each owner as an email, like ana@example.com.`
  if (invalid.length > 1) {
    const quoted = invalid.map((email) => `"${email}"`).join(', ')
    return `${quoted} are not emails. Type each owner as an email, like ana@example.com.`
  }
  if (owners.length > MAX_OWNERS) return `Owners takes at most ${MAX_OWNERS} emails. Remove ${owners.length - MAX_OWNERS}.`
  return null
}

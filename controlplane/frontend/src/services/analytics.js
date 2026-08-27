/* global __SEGMENT_WRITE_KEY__ */
import { AnalyticsBrowser } from '@segment/analytics-next'

const SEGMENT_WRITE_KEY = __SEGMENT_WRITE_KEY__

let analytics = null

function load() {
  if (analytics) return analytics
  analytics = AnalyticsBrowser.load({ writeKey: SEGMENT_WRITE_KEY })
  return analytics
}

async function sha256Hex(input) {
  const bytes = new TextEncoder().encode(input)
  const buf = await crypto.subtle.digest('SHA-256', bytes)
  return Array.from(new Uint8Array(buf))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

function buildTraits(user, mode, hashedId) {
  const traits = {
    'org-id': user.org_id,
    'user-id': hashedId,
    'is-admin': !!user.is_admin,
    status: user.status,
  }
  if (mode === 'identified') {
    traits.email = user.email
    traits.name = user.name
  }
  return traits
}

// Identifies the current user in Segment so the browser's anonymous_id is
// linked to the same user_id the gateway uses (SHA-256 of the OIDC subject).
// Without this, every browser session is counted as a separate MTU. See ENG-407.
export async function identify(user, mode) {
  if (!user?.id || mode === 'disabled') return
  const instance = load()
  const hashedId = await sha256Hex(user.id)
  // analytics-next persists traits to localStorage and merges them into every
  // subsequent identify() call. Without resetting, email/name from a prior
  // 'identified' session would leak into anonymous-mode payloads. Replacing
  // the cache here guarantees the wire payload matches buildTraits() exactly.
  const userObj = await instance.user()
  userObj.traits({})
  await instance.identify(hashedId, buildTraits(user, mode, hashedId))
}

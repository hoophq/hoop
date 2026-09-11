// The control plane's first-access license screen. Shared by the guard that
// sends an admin there (components/ControlPlaneProtectedRoute) and the page
// that lets them out (pages/Onboarding/License).

export const LICENSE_INTRO_PATH = '/onboarding/license'

// Keyed by user, like the LicenseBanner dismissal: one admin cannot silence
// another, and two admins sharing a browser each keep their own answer. Keyed
// by user only: an expired or invalid license has the red banner with its own
// dismissal, and once a sidecar exists the intro never returns.
const SKIP_KEY = 'control-plane-license-intro-skipped-for'

function skippedIds() {
  try {
    const raw = localStorage.getItem(SKIP_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    // A single id is what earlier versions stored; read it as a list of one.
    return Array.isArray(parsed) ? parsed : [parsed]
  } catch {
    // Absent, blocked, or holding a bare id that is not JSON: no skip on file.
    return []
  }
}

export function hasSkippedLicenseIntro(userId) {
  if (!userId) return false
  return skippedIds().includes(userId)
}

export function skipLicenseIntro(userId) {
  if (!userId) return
  try {
    const ids = skippedIds()
    if (ids.includes(userId)) return
    localStorage.setItem(SKIP_KEY, JSON.stringify([...ids, userId]))
  } catch {
    // Storage unavailable (private mode, quota): the intro shows again next time.
  }
}

// The control plane's first-access license screen. Shared by the guard that
// sends an admin there (components/ControlPlaneProtectedRoute) and the page
// that lets them out (pages/Onboarding/License).

export const LICENSE_INTRO_PATH = '/onboarding/license'

// Keyed by user, like the LicenseBanner dismissal: one admin cannot silence
// another. Keyed by user only: an expired or invalid license has the red banner
// with its own dismissal, and once a sidecar exists the intro never returns.
const SKIP_KEY = 'control-plane-license-intro-skipped-for'

export function hasSkippedLicenseIntro(userId) {
  if (!userId) return false
  try {
    return localStorage.getItem(SKIP_KEY) === userId
  } catch {
    return false
  }
}

export function skipLicenseIntro(userId) {
  try {
    localStorage.setItem(SKIP_KEY, userId)
  } catch {
    // Storage unavailable (private mode, quota): the intro shows again next time.
  }
}

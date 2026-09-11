import { useLocation } from 'react-router-dom'
import ProtectedRoute from './ProtectedRoute'
import { sidecarsService } from '@/services/sidecars'
import { useUserStore } from '@/stores/useUserStore'
import { LICENSE_INTRO_PATH, hasSkippedLicenseIntro } from '@/utils/licenseIntro'

// The control plane's gate, sibling of GatewayProtectedRoute: the shared
// ProtectedRoute plus the first-access license screen. An admin on the free
// plan with no sidecar yet is sent to /onboarding/license once; "I don't have a
// license" remembers the choice per user. Skipped on the onboarding routes
// themselves to avoid a redirect loop.
function ControlPlaneProtectedRoute(props) {
  const location = useLocation()
  const isOnboardingRoute = location.pathname.startsWith('/onboarding')

  const onReady = async (user) => {
    // ProtectedRoute has already run setUser and setServerInfo (or retried it)
    // when onReady is called, so the store is current here. ProtectedRoute
    // applies `redirectTo` before `adminOnly`, hence the admin check first.
    const { isAdmin, serverInfoLoaded, isFreeLicense } = useUserStore.getState()
    if (!isAdmin || isOnboardingRoute) return null
    // Without /serverinfo, isFreeLicense is its default `true`: an outage must
    // not read as a missing license.
    if (!serverInfoLoaded || !isFreeLicense) return null
    if (hasSkippedLicenseIntro(user.id)) return null
    try {
      // Read straight from the service, not through useSidecarStore: this runs
      // before any page mounts and answers one question, "does this org own a
      // sidecar". Routing it through the fleet store would make the gate
      // populate application state as a side effect of a redirect decision.
      const { data } = await sidecarsService.list()
      if ((data ?? []).length === 0) return LICENSE_INTRO_PATH
    } catch {
      // On API error, let the user through rather than blocking access.
    }
    return null
  }

  return <ProtectedRoute {...props} onReady={onReady} />
}

export default ControlPlaneProtectedRoute

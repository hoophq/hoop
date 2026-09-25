import { useLocation } from 'react-router-dom'
import ProtectedRoute from './ProtectedRoute'
import { useUserStore } from '@/stores/useUserStore'
import { LICENSE_INTRO_PATH } from '@/utils/licenseIntro'

// The control plane's gate, sibling of GatewayProtectedRoute: the shared
// ProtectedRoute plus the license screen. An admin without a valid Enterprise
// license (none, OSS, expired, invalid) is sent to /onboarding/license from
// every protected route. Skipped on the onboarding routes themselves to avoid
// a redirect loop.
function ControlPlaneProtectedRoute(props) {
  const location = useLocation()
  const isOnboardingRoute = location.pathname.startsWith('/onboarding')

  const onReady = () => {
    // ProtectedRoute has already run setUser and setServerInfo (or retried it)
    // when onReady is called, so the store is current here. ProtectedRoute
    // applies `redirectTo` before `adminOnly`, hence the admin check first.
    const { isAdmin, serverInfoLoaded, isFreeLicense } = useUserStore.getState()
    if (!isAdmin || isOnboardingRoute) return null
    // Without /serverinfo, isFreeLicense is its default `true`: an outage must
    // not read as a missing license.
    if (!serverInfoLoaded || !isFreeLicense) return null
    return LICENSE_INTRO_PATH
  }

  return <ProtectedRoute {...props} onReady={onReady} />
}

export default ControlPlaneProtectedRoute

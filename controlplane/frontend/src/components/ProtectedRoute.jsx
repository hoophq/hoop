import { useEffect, useRef, useState } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { authService } from '@/services/auth'
import { featureFlagsService } from '@/services/featureFlags'
import AuthPageLoader from '@/components/AuthPageLoader'

function ProtectedRoute({ children, adminOnly = false, licenseFeature = null }) {
  const location = useLocation()
  const { isAuthenticated, saveRedirectUrl, logout } = useAuthStore()
  const { user, isAdmin, setUser, setLoading, setServerInfo, setFeatureFlags, initIntercom, initAnalytics } = useUserStore()
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)
  const [initializing, setInitializing] = useState(true)
  const [redirectTo, setRedirectTo] = useState(null)
  const initialized = useRef(false)

  useEffect(() => {
    // Run only once per component instance — prevents StrictMode double-fire
    // and avoids re-checking on every location change within the same route.
    if (initialized.current) return
    initialized.current = true

    if (!isAuthenticated) {
      saveRedirectUrl(window.location.href)
      setRedirectTo('/login')
      setInitializing(false)
      return
    }

    const initialize = async () => {
      try {
        // Fetch user if not already in store
        let currentUser = user
        if (!currentUser) {
          setLoading(true)
          const [userData, serverInfo] = await Promise.all([
            authService.getCurrentUser(),
            authService.getServerInfo().catch(() => null),
          ])
          setLoading(false)

          if (!userData || Object.keys(userData).length === 0) {
            saveRedirectUrl(window.location.href)
            logout()
            setRedirectTo('/login')
            return
          }

          setUser(userData)
          if (serverInfo) {
            setServerInfo(serverInfo)
            initIntercom(userData)
            initAnalytics(userData)
          }
          currentUser = userData
        }

        // Retry /serverinfo when a previous fetch failed or was skipped:
        // license gating fails closed without it, so gated routes would
        // otherwise stay blocked until a full reload.
        if (!useUserStore.getState().serverInfoLoaded) {
          const serverInfo = await authService.getServerInfo().catch(() => null)
          if (serverInfo) setServerInfo(serverInfo)
        }

        // Get feature flag
        const { data: featureFlagsData } = await featureFlagsService.list()
        const featureFlags = Object.fromEntries(featureFlagsData.map(flag => [flag.name, flag.enabled]))
        setFeatureFlags(featureFlags)

        // No onboarding gate. The gateway sent an admin with zero connections into a
        // setup wizard; the control plane has no equivalent — its first-run journey
        // belongs to the One Command Start and Admin Authentication projects. Until
        // then a fresh organization lands on /sidecars, whose empty state already says
        // the first step is connecting a sidecar. Redirecting to a route this app does
        // not have would strand exactly the user we most need to get through.
      } catch (error) {
        console.error('[ProtectedRoute] initialization failed:', error)
        saveRedirectUrl(window.location.href)
        logout()
        setRedirectTo('/login')
      } finally {
        setInitializing(false)
      }
    }

    initialize()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // React Router reuses this ProtectedRoute instance when navigating between Route
  // entries whose element tree starts with <ProtectedRoute>. That means redirectTo
  // state survives the navigation it
  // triggered, and on the next render — with the new pathname — we'd return
  // <Navigate> again, firing history.replace in a loop until the browser
  // throttles. Clear redirectTo once we've arrived at the target.
  useEffect(() => {
    if (redirectTo && location.pathname === redirectTo) {
      setRedirectTo(null)
    }
  }, [location.pathname, redirectTo])

  if (redirectTo && location.pathname !== redirectTo) {
    return <Navigate to={redirectTo} state={{ from: location }} replace />
  }

  if (initializing) {
    return <AuthPageLoader message="Verifying authentication..." />
  }

  if (adminOnly && !isAdmin) {
    return <Navigate to="/" replace />
  }

  // License gating: fails closed — while /serverinfo is unknown the check
  // returns false. Once loaded, an empty feature list means everything is
  // enabled; otherwise the feature key must be present.
  if (licenseFeature && !isLicenseFeatureEnabled(licenseFeature)) {
    return <Navigate to="/" replace />
  }

  return children
}

export default ProtectedRoute

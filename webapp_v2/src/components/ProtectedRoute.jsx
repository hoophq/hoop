import { useEffect, useRef, useState } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { authService } from '@/services/auth'
import { connectionsService } from '@/services/connections'
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
  const serverInfoRetriedPath = useRef(null)

  const isOnboardingRoute = location.pathname.startsWith('/onboarding')

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

        // Check onboarding: admin with no connections must go through onboarding.
        // Skip if already on onboarding routes to avoid a redirect loop.
        if (currentUser.is_admin && !isOnboardingRoute) {
          try {
            const { pages } = await connectionsService.getConnectionsPaginated({ pageSize: 1 })
            if ((pages?.total ?? 0) === 0) {
              // Protection rules come first: until a profile has been applied
              // (default_protection_profile is null for both "never chose" and
              // "manual"), onboarding starts at the protection-rules step.
              setRedirectTo(
                currentUser.default_protection_profile
                  ? '/onboarding/setup'
                  : '/onboarding/protection-rules'
              )
              return
            }
          } catch {
            // On API error, let the user through rather than blocking access.
          }
        }
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

  // /serverinfo self-heal. The effect above runs once per ProtectedRoute
  // instance and React Router reuses that instance across navigations, so its
  // retry never fires again — a transient /serverinfo failure would leave every
  // control derived from it (license gating, the clipboard guard) off for the
  // whole session. The CLJS app got this for free by refetching /serverinfo
  // from six places; React has to ask again explicitly.
  useEffect(() => {
    if (!isAuthenticated || initializing) return

    // initialize() already retried for the path we mounted on; only ask again
    // once the user actually navigates, so a failing gateway gets one request
    // per navigation rather than a burst.
    const previousPath = serverInfoRetriedPath.current
    serverInfoRetriedPath.current = location.pathname
    if (previousPath === null || previousPath === location.pathname) return

    if (useUserStore.getState().serverInfoLoaded) return

    authService
      .getServerInfo()
      .then((serverInfo) => {
        if (serverInfo) setServerInfo(serverInfo)
      })
      .catch(() => {
        // Still unavailable — try again on the next navigation.
      })
  }, [location.pathname, isAuthenticated, initializing, setServerInfo])

  // React Router reuses this ProtectedRoute instance when navigating between
  // Route entries whose element tree starts with <ProtectedRoute> (both /* and
  // /onboarding/*). That means redirectTo state survives the navigation it
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

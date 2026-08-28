import { useEffect, useRef, useState } from 'react'
import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { authService } from '@/services/auth'
import { featureFlagsService } from '@/services/featureFlags'
import AuthPageLoader from '@/components/AuthPageLoader'

function ProtectedRoute({ children, adminOnly = false }) {
  const location = useLocation()
  const { isAuthenticated, saveRedirectUrl, logout } = useAuthStore()
  const { user, isAdmin, setUser, setLoading, setServerInfo, setFeatureFlags, initIntercom, initAnalytics } = useUserStore()
  // Only block on the bootstrap when there is nothing to render with yet.
  //
  // `initialized` is a ref, so it guards one instance. Two live in the tree —
  // `/` wraps Home directly, AppLayout wraps everything else — and landing on
  // `/` as an admin mounts the first, redirects to /sidecars, and mounts the
  // second. Starting at `true` unconditionally made that second mount show the
  // dark auth loader again, for the length of a feature-flags round trip, on
  // the most common path in the app.
  //
  // A user already in the store means the session is bootstrapped. The effect
  // below still runs and still refreshes the flags; it just does not hold the
  // screen hostage while it does.
  const [initializing, setInitializing] = useState(() => !useUserStore.getState().user)
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

        // Feature flags, guarded like /serverinfo above and for the same reason.
        // Everything in this try shares one catch, and that catch logs the user
        // out — correct for "we cannot say who you are", wrong for "one request
        // blipped". A flaky /feature-flags used to end the session.
        //
        // Skipping it is safe: setServerInfo already seeded featureFlags from
        // /serverinfo, and isFeatureFlagEnabled fails closed on anything it does
        // not find. The worst case is a flag reading off, not a logout.
        const featureFlagsData = await featureFlagsService
          .list()
          .then((res) => res.data)
          .catch(() => null)
        if (featureFlagsData) {
          setFeatureFlags(Object.fromEntries(featureFlagsData.map((f) => [f.name, f.enabled])))
        }

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

  // Licence gating is NOT here. It lives in components/LicenseRoute, because
  // this component bootstraps the session and a nested second instance would
  // bootstrap again — the dark auth loader flashing inside the app shell on
  // every entry into a gated group. One bootstrap, in AppLayout; the licence
  // check is a synchronous gate below it.

  // Works both ways: wrap a page directly, or sit on a pathless <Route> as a
  // layout and let the matched child render through the outlet. The second
  // form is how a whole group of routes shares one gate — see Router.jsx.
  return children ?? <Outlet />
}

export default ProtectedRoute

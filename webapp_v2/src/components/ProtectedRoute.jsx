import { useEffect, useRef, useState } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { authService } from '@/services/auth'
import { connectionsService } from '@/services/connections'
import PageLoader from '@/components/PageLoader'

function ProtectedRoute({ children }) {
  const location = useLocation()
  const { isAuthenticated, saveRedirectUrl, logout } = useAuthStore()
  const { user, setUser, setLoading, setServerInfo } = useUserStore()
  const [initializing, setInitializing] = useState(true)
  const [redirectTo, setRedirectTo] = useState(null)
  const initialized = useRef(false)

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
          if (serverInfo) setServerInfo(serverInfo)
          currentUser = userData
        }

        // Check onboarding: admin with no connections must go through onboarding.
        // Skip if already on onboarding routes to avoid a redirect loop.
        if (currentUser.is_admin && !isOnboardingRoute) {
          try {
            const data = await connectionsService.getConnections()
            const list = Array.isArray(data) ? data : (data?.items ?? data?.data ?? [])
            if (list.length === 0) {
              setRedirectTo('/onboarding/setup')
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

  if (redirectTo) {
    return <Navigate to={redirectTo} state={{ from: location }} replace />
  }

  if (initializing) {
    return <PageLoader message="Verifying authentication..." />
  }

  return children
}

export default ProtectedRoute

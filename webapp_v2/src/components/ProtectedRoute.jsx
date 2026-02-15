import { useEffect, useState } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { Center, Loader } from '@mantine/core'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { authService } from '@/services/auth'

/**
 * ProtectedRoute component that ensures user is authenticated before rendering children
 * Follows webapp logic:
 * 1. Check if token exists
 * 2. If no token, save current URL and redirect to login
 * 3. If token exists, fetch user data
 * 4. If user data is empty/invalid, clear token and redirect to login
 * 5. If all checks pass, render children (protected content)
 */
function ProtectedRoute({ children }) {
  const location = useLocation()
  const { isAuthenticated, saveRedirectUrl, logout } = useAuthStore()
  const { user, loading, setUser, setLoading } = useUserStore()
  const [shouldRedirect, setShouldRedirect] = useState(false)

  useEffect(() => {
    // If not authenticated, save URL and redirect to login
    if (!isAuthenticated) {
      saveRedirectUrl(window.location.href)
      setShouldRedirect(true)
      return
    }

    // If already have user data, no need to fetch again
    if (user) {
      return
    }

    // Fetch user data
    const fetchUser = async () => {
      setLoading(true)
      try {
        const userData = await authService.getCurrentUser()

        if (!userData || Object.keys(userData).length === 0) {
          // User data is empty, token is invalid
          saveRedirectUrl(window.location.href)
          logout()
          setShouldRedirect(true)
          return
        }

        setUser(userData)
      } catch (error) {
        console.error('Failed to fetch user:', error)
        // On error, logout and redirect
        saveRedirectUrl(window.location.href)
        logout()
        setShouldRedirect(true)
      } finally {
        setLoading(false)
      }
    }

    fetchUser()
  }, [isAuthenticated, user, setUser, setLoading, saveRedirectUrl, logout])

  // Redirect to login if needed
  if (shouldRedirect || !isAuthenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  // Show loading while fetching user
  if (loading || !user) {
    return (
      <Center style={{ height: '100vh' }}>
        <Loader size="lg" />
      </Center>
    )
  }

  // User is authenticated and loaded, render protected content
  return children
}

export default ProtectedRoute

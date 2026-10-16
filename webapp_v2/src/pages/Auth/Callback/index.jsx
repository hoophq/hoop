import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/stores/useAuthStore'
import AuthPageLoader from '@/components/AuthPageLoader'
import { postAuthPath } from '@/modes'

function AuthCallback() {
  const navigate = useNavigate()
  const { initializeToken, getAndClearRedirectUrl } = useAuthStore()
  const [error, setError] = useState(null)
  // The callback runs once. A second run (StrictMode's double effect) finds
  // the saved URL already taken and would send the app to the default page,
  // racing the first run's redirect.
  const handled = useRef(false)

  useEffect(() => {
    if (handled.current) return
    handled.current = true
    const handleCallback = async () => {
      try {
        const searchParams = new URLSearchParams(window.location.search)
        const errorParam = searchParams.get('error')

        if (errorParam) {
          localStorage.setItem('login_error', errorParam)
          setError(errorParam)
          setTimeout(() => navigate('/login'), 2000)
          return
        }

        const token = initializeToken()

        if (!token) {
          setError('No authentication token received')
          setTimeout(() => navigate('/login'), 2000)
          return
        }

        const redirectUrl = getAndClearRedirectUrl()
        if (redirectUrl) {
          // A page of this app goes back in place: no reload to race.
          const saved = new URL(redirectUrl, window.location.origin)
          if (saved.origin === window.location.origin) {
            navigate(saved.pathname + saved.search + saved.hash, { replace: true })
          } else {
            window.location.href = redirectUrl
          }
        } else {
          navigate(await postAuthPath())
        }
      } catch (err) {
        console.error('Auth callback error:', err)
        setError('Authentication failed')
        setTimeout(() => navigate('/login'), 2000)
      }
    }

    handleCallback()
  }, [navigate, initializeToken, getAndClearRedirectUrl])

  return error ? (
    <AuthPageLoader
      error
      message="Authentication failed"
      description="Redirecting to login..."
    />
  ) : (
    <AuthPageLoader
      message="Verifying authentication..."
      description="Please wait while we complete your sign in"
    />
  )
}

export default AuthCallback

import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/stores/useAuthStore'
import AuthPageLoader from '@/components/AuthPageLoader'
import { postAuthPath } from '@/modes'
import { samePagePath } from '@/utils/redirect'

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
          localStorage.setItem('login_error', 'No authentication token received')
          setError('No authentication token received')
          setTimeout(() => navigate('/login'), 2000)
          return
        }

        // ?next= is the page the login started from, carried through the IdP
        // in this URL (Login's redirectToIdp). It wins over storage, which a
        // browser or another tab can lose or replace on the way.
        const saved = getAndClearRedirectUrl()
        const target = samePagePath(searchParams.get('next')) || samePagePath(saved)
        if (target) {
          // A page of this app goes back in place: no reload to race.
          navigate(target, { replace: true })
        } else if (saved) {
          window.location.href = saved
        } else {
          navigate(await postAuthPath())
        }
      } catch (err) {
        console.error('Auth callback error:', err)
        localStorage.setItem('login_error', 'Authentication failed')
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

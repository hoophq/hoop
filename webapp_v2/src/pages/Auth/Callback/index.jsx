import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/stores/useAuthStore'
import PageLoader from '@/components/PageLoader'

function AuthCallback() {
  const navigate = useNavigate()
  const { initializeToken, getAndClearRedirectUrl } = useAuthStore()
  const [error, setError] = useState(null)

  useEffect(() => {
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
        setTimeout(() => {
          if (redirectUrl) {
            window.location.href = redirectUrl
          } else {
            navigate('/')
          }
        }, 1500)
      } catch (err) {
        console.error('Auth callback error:', err)
        setError('Authentication failed')
        setTimeout(() => navigate('/login'), 2000)
      }
    }

    handleCallback()
  }, [navigate, initializeToken, getAndClearRedirectUrl])

  return error ? (
    <PageLoader
      error
      message="Authentication failed"
      description="Redirecting to login..."
    />
  ) : (
    <PageLoader
      message="Verifying authentication..."
      description="Please wait while we complete your sign in"
    />
  )
}

export default AuthCallback

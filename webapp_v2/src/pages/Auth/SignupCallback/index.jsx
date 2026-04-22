import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/stores/useAuthStore'
import PageLoader from '@/components/PageLoader'

function SignupCallback() {
  const navigate = useNavigate()
  const { initializeToken } = useAuthStore()
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

        setTimeout(() => navigate('/signup'), 1500)
      } catch (err) {
        console.error('Signup callback error:', err)
        setError('Authentication failed')
        setTimeout(() => navigate('/login'), 2000)
      }
    }

    handleCallback()
  }, [navigate, initializeToken])

  return error ? (
    <PageLoader
      error
      message="Authentication failed"
      description="Redirecting to login..."
    />
  ) : (
    <PageLoader
      message="Setting up your account..."
      description="Please wait while we complete your sign up"
    />
  )
}

export default SignupCallback

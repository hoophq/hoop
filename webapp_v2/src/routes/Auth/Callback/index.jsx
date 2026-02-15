import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Container, Paper, Title, Text, Loader, Center, Alert, Stack } from '@mantine/core'
import { useAuthStore } from '@/stores/useAuthStore'

function AuthCallback() {
  const navigate = useNavigate()
  const { initializeToken, getAndClearRedirectUrl } = useAuthStore()
  const [error, setError] = useState(null)

  useEffect(() => {
    const handleCallback = async () => {
      try {
        // Check for error in query params
        const searchParams = new URLSearchParams(window.location.search)
        const errorParam = searchParams.get('error')

        if (errorParam) {
          // Save error to localStorage for login page to display
          localStorage.setItem('login_error', errorParam)
          setError(errorParam)

          // Redirect to login after delay
          setTimeout(() => {
            navigate('/login')
          }, 2000)
          return
        }

        // Initialize token from cookies or query params
        const token = initializeToken()

        if (!token) {
          setError('No authentication token received')
          setTimeout(() => {
            navigate('/login')
          }, 2000)
          return
        }

        // Token successfully initialized, redirect to app
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
        setTimeout(() => {
          navigate('/login')
        }, 2000)
      }
    }

    handleCallback()
  }, [navigate, initializeToken, getAndClearRedirectUrl])

  return (
    <Container size={420} my={40}>
      <Paper withBorder shadow="md" p={30} mt={30} radius="md">
        <Stack align="center" spacing="md">
          <Title order={2} ta="center">
            {error ? 'Authentication Failed' : 'Verifying authentication...'}
          </Title>

          {error ? (
            <>
              <Alert color="red" style={{ width: '100%' }}>
                {error}
              </Alert>
              <Text size="sm" c="dimmed">
                Redirecting to login...
              </Text>
            </>
          ) : (
            <>
              <Loader size="lg" />
              <Text size="sm" c="dimmed">
                Please wait while we complete your authentication
              </Text>
            </>
          )}
        </Stack>
      </Paper>
    </Container>
  )
}

export default AuthCallback

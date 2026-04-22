import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Container, Paper, Title, Text, Loader, Center, Alert, Stack } from '@mantine/core'
import { useAuthStore } from '@/stores/useAuthStore'

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

        // Token stored — redirect to org setup page
        setTimeout(() => navigate('/signup'), 1500)
      } catch (err) {
        console.error('Signup callback error:', err)
        setError('Authentication failed')
        setTimeout(() => navigate('/login'), 2000)
      }
    }

    handleCallback()
  }, [navigate, initializeToken])

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

export default SignupCallback

import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Container,
  Paper,
  Title,
  TextInput,
  PasswordInput,
  Button,
  Text,
  Anchor,
  Stack,
  Center,
  Loader,
  Alert,
} from '@mantine/core'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { authService } from '@/services/auth'

function Login() {
  const navigate = useNavigate()
  const { setToken, getAndClearRedirectUrl, isAuthenticated } = useAuthStore()
  const { setUser } = useUserStore()

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [authMethod, setAuthMethod] = useState(null)
  const [loadingAuthMethod, setLoadingAuthMethod] = useState(true)

  // Check if user is already authenticated
  useEffect(() => {
    if (isAuthenticated) {
      const redirectUrl = getAndClearRedirectUrl()
      if (redirectUrl) {
        window.location.href = redirectUrl
      } else {
        navigate('/')
      }
    }
  }, [isAuthenticated, navigate, getAndClearRedirectUrl])

  // Fetch auth method from gateway info
  useEffect(() => {
    const fetchAuthMethod = async () => {
      try {
        // Fetch public gateway info to determine auth method
        const response = await fetch('/api/info/public')
        const data = await response.json()
        setAuthMethod(data.auth_method || 'local')
      } catch (err) {
        console.error('Failed to fetch auth method:', err)
        setAuthMethod('local') // Default to local
      } finally {
        setLoadingAuthMethod(false)
      }
    }

    fetchAuthMethod()
  }, [])

  // Handle local auth login
  const handleLocalLogin = async (e) => {
    e.preventDefault()
    setError(null)
    setLoading(true)

    try {
      const { token, user } = await authService.loginLocal(email, password)
      setToken(token)
      setUser(user)

      const redirectUrl = getAndClearRedirectUrl()
      if (redirectUrl) {
        window.location.href = redirectUrl
      } else {
        navigate('/')
      }
    } catch (err) {
      setError(err.response?.data?.message || 'Invalid email or password')
    } finally {
      setLoading(false)
    }
  }

  // Handle IDP login redirect
  const handleIdpLogin = async () => {
    setLoading(true)
    try {
      const callbackUrl = `${window.location.origin}/auth/callback`
      const loginUrl = await authService.getLoginUrl(callbackUrl)
      window.location.href = loginUrl
    } catch (err) {
      setError('Failed to initialize login')
      setLoading(false)
    }
  }

  if (loadingAuthMethod) {
    return (
      <Container size={420} my={40}>
        <Center style={{ height: '50vh' }}>
          <Loader size="lg" />
        </Center>
      </Container>
    )
  }

  // IDP Login (OAuth)
  if (authMethod !== 'local') {
    return (
      <Container size={420} my={40}>
        <Paper withBorder shadow="md" p={30} mt={30} radius="md">
          <Title order={2} ta="center" mb="lg">
            Welcome to Hoop
          </Title>

          {error && (
            <Alert color="red" mb="md">
              {error}
            </Alert>
          )}

          <Button fullWidth onClick={handleIdpLogin} loading={loading}>
            Sign in with SSO
          </Button>
        </Paper>
      </Container>
    )
  }

  // Local Login (email/password)
  return (
    <Container size={420} my={40}>
      <Paper withBorder shadow="md" p={30} mt={30} radius="md">
        <Title order={2} ta="center" mb="lg">
          Welcome back
        </Title>

        {error && (
          <Alert color="red" mb="md">
            {error}
          </Alert>
        )}

        <form onSubmit={handleLocalLogin}>
          <Stack>
            <TextInput
              label="Email"
              placeholder="your@email.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              type="email"
            />

            <PasswordInput
              label="Password"
              placeholder="Your password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />

            <Button type="submit" fullWidth loading={loading}>
              Sign in
            </Button>
          </Stack>
        </form>

        <Text c="dimmed" size="sm" ta="center" mt="md">
          Don't have an account?{' '}
          <Anchor size="sm" onClick={() => navigate('/signup')}>
            Create account
          </Anchor>
        </Text>
      </Paper>
    </Container>
  )
}

export default Login

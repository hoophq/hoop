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

// Map error codes from backend to human-readable messages
const LOGIN_ERROR_MESSAGES = {
  slack_not_configured: 'You must configure your Slack with Hoop',
  code_exchange_failure:
    'Something went wrong. Try again and if the error persists, talk to the account administrator',
  pending_review: 'The organization administrator must approve your access first',
  unregistered:
    'Your user is not registered. Try to signup or talk to the account administrator',
}

function getLoginErrorMessage(error) {
  return LOGIN_ERROR_MESSAGES[error] || error
}

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

  // Check for stored login error (set by callback page)
  useEffect(() => {
    const loginError = localStorage.getItem('login_error')
    if (loginError) {
      setError(getLoginErrorMessage(loginError))
      localStorage.removeItem('login_error')
    }
  }, [])

  // Fetch auth method from /publicserverinfo
  useEffect(() => {
    if (isAuthenticated) return

    const fetchAuthMethod = async () => {
      try {
        const serverInfo = await authService.getPublicServerInfo()
        const method = serverInfo.auth_method || 'local'
        setAuthMethod(method)

        // If not local auth, redirect immediately to OAuth provider
        if (method !== 'local') {
          redirectToIdp()
        }
      } catch (err) {
        console.error('Failed to fetch server info:', err)
        setAuthMethod('local')
      } finally {
        setLoadingAuthMethod(false)
      }
    }

    fetchAuthMethod()
  }, [isAuthenticated])

  // Redirect to IDP (OAuth) provider
  const redirectToIdp = async (options = {}) => {
    setLoading(true)
    try {
      const callbackUrl = `${window.location.origin}/auth/callback`
      const loginUrl = await authService.getLoginUrl(callbackUrl, options)
      window.location.replace(loginUrl)
    } catch (err) {
      setError('Failed to initialize login')
      setLoading(false)
    }
  }

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

  // Handle signup redirect
  const handleSignup = async () => {
    if (authMethod === 'local') {
      navigate('/signup')
      return
    }

    // For IDP, redirect to signup URL
    try {
      const callbackUrl = `${window.location.origin}/auth/callback`
      const signupUrl = await authService.getSignupUrl(callbackUrl)
      window.location.replace(signupUrl)
    } catch (err) {
      setError('Failed to initialize signup')
    }
  }

  // Loading state while determining auth method
  if (loadingAuthMethod || (authMethod !== 'local' && !error)) {
    return (
      <Center style={{ height: '100vh' }}>
        <Stack align="center">
          <Loader size="lg" />
          <Text size="sm" c="dimmed">
            Redirecting to login...
          </Text>
        </Stack>
      </Center>
    )
  }

  // If OAuth failed to redirect, show a fallback button
  if (authMethod !== 'local' && error) {
    return (
      <Container size={420} my={40}>
        <Paper withBorder shadow="md" p={30} mt={30} radius="md">
          <Title order={2} ta="center" mb="lg">
            Welcome to Hoop
          </Title>

          <Alert color="red" mb="md">
            {error}
          </Alert>

          <Button fullWidth onClick={() => redirectToIdp({ promptLogin: true })} loading={loading}>
            Try again
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
          Sign in to your account
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
          <Anchor size="sm" onClick={handleSignup}>
            Sign Up
          </Anchor>
        </Text>
      </Paper>
    </Container>
  )
}

export default Login

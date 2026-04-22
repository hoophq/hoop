import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Paper,
  Title,
  TextInput,
  PasswordInput,
  Button,
  Text,
  Anchor,
  Stack,
  Alert,
  Center,
  Box,
} from '@mantine/core'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { authService } from '@/services/auth'
import PageLoader from '@/components/PageLoader'

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

function AuthCard({ children }) {
  return (
    <Center style={{ minHeight: '100vh', backgroundColor: 'var(--mantine-color-gray-1)' }}>
      <Box style={{ width: '90%', maxWidth: 400 }}>
        <Paper shadow="sm" p={40} radius="lg">
          <img
            src="/images/hoop-branding/SVG/hoop-symbol_black.svg"
            alt="hoop"
            style={{ width: 48, display: 'block', margin: '0 auto 24px' }}
          />
          {children}
        </Paper>
      </Box>
    </Center>
  )
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

  useEffect(() => {
    const loginError = localStorage.getItem('login_error')
    if (loginError) {
      setError(getLoginErrorMessage(loginError))
      localStorage.removeItem('login_error')
    }
  }, [])

  useEffect(() => {
    if (isAuthenticated) return

    const fetchAuthMethod = async () => {
      try {
        const serverInfo = await authService.getPublicServerInfo()
        const method = serverInfo.auth_method || 'local'
        setAuthMethod(method)

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

  const handleSignup = async () => {
    if (authMethod === 'local') {
      navigate('/register')
      return
    }

    try {
      const callbackUrl = `${window.location.origin}/signup/callback`
      const signupUrl = await authService.getSignupUrl(callbackUrl)
      window.location.replace(signupUrl)
    } catch (err) {
      setError('Failed to initialize signup')
    }
  }

  if (loadingAuthMethod || (authMethod !== 'local' && !error)) {
    return <PageLoader message="Redirecting to login..." />
  }

  if (authMethod !== 'local' && error) {
    return (
      <AuthCard>
        <Title order={2} ta="center" mb="lg">
          Welcome to Hoop
        </Title>
        <Alert color="red" mb="md">
          {error}
        </Alert>
        <Button fullWidth onClick={() => redirectToIdp({ promptLogin: true })} loading={loading}>
          Try again
        </Button>
      </AuthCard>
    )
  }

  return (
    <AuthCard>
      <Title order={2} ta="center" mb="lg">
        Login
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
            placeholder="Email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            type="email"
          />

          <PasswordInput
            label="Password"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />

          <Button type="submit" fullWidth loading={loading}>
            Login
          </Button>
        </Stack>
      </form>

      <Text c="dimmed" size="sm" ta="center" mt="md">
        Don&apos;t have an account?{' '}
        <Anchor size="sm" onClick={handleSignup}>
          Create one
        </Anchor>
      </Text>
    </AuthCard>
  )
}

export default Login

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

function Register() {
  const navigate = useNavigate()
  const { setToken, isAuthenticated } = useAuthStore()
  const { setUser } = useUserStore()

  const [fullName, setFullName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [passwordError, setPasswordError] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [checkingAuthMethod, setCheckingAuthMethod] = useState(true)

  useEffect(() => {
    if (isAuthenticated) {
      navigate('/')
      return
    }

    const checkAuthMethod = async () => {
      try {
        const serverInfo = await authService.getPublicServerInfo()
        if (serverInfo.auth_method && serverInfo.auth_method !== 'local') {
          navigate('/login')
        }
      } catch {
        // If we can't check, assume local and stay
      } finally {
        setCheckingAuthMethod(false)
      }
    }

    checkAuthMethod()
  }, [isAuthenticated, navigate])

  const validatePasswords = () => {
    if (password !== confirmPassword) {
      setPasswordError('Passwords do not match')
      return false
    }
    setPasswordError(null)
    return true
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!validatePasswords()) return

    setError(null)
    setLoading(true)

    try {
      const { token, user } = await authService.registerLocal(email, password, fullName)
      setToken(token)
      setUser(user)
      navigate('/')
    } catch (err) {
      setError(err.response?.data?.message || 'Registration failed. Please try again.')
    } finally {
      setLoading(false)
    }
  }

  if (checkingAuthMethod) {
    return <PageLoader />
  }

  return (
    <Center style={{ minHeight: '100vh', backgroundColor: 'var(--mantine-color-gray-1)' }}>
      <Box style={{ width: '90%', maxWidth: 400 }}>
        <Paper shadow="sm" p={40} radius="lg">
          <img
            src="/images/hoop-branding/SVG/hoop-symbol_black.svg"
            alt="hoop"
            style={{ width: 48, display: 'block', margin: '0 auto 24px' }}
          />

          <Title order={2} ta="center" mb="lg">
            Create an account
          </Title>

          {error && (
            <Alert color="red" mb="md">
              {error}
            </Alert>
          )}

          <form onSubmit={handleSubmit}>
            <Stack>
              <TextInput
                label="Full name"
                placeholder="Full name"
                value={fullName}
                onChange={(e) => setFullName(e.target.value)}
                required
              />

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

              <PasswordInput
                label="Confirm Password"
                placeholder="Confirm Password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                onBlur={validatePasswords}
                required
                error={passwordError}
              />

              <Button type="submit" fullWidth loading={loading}>
                Register
              </Button>
            </Stack>
          </form>

          <Text c="dimmed" size="sm" ta="center" mt="md">
            Already have an account?{' '}
            <Anchor size="sm" component="a" href="/login">
              Login
            </Anchor>
          </Text>
        </Paper>
      </Box>
    </Center>
  )
}

export default Register

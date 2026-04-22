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
    return (
      <Center style={{ height: '100vh' }}>
        <Loader size="lg" />
      </Center>
    )
  }

  return (
    <Container size={420} my={40}>
      <Paper withBorder shadow="md" p={30} mt={30} radius="md">
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
              placeholder="Michael Scott"
              value={fullName}
              onChange={(e) => setFullName(e.target.value)}
              required
            />

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

            <PasswordInput
              label="Confirm Password"
              placeholder="Repeat your password"
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
    </Container>
  )
}

export default Register

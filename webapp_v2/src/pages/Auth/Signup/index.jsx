import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Container,
  Paper,
  Title,
  TextInput,
  Button,
  Text,
  Anchor,
  Stack,
  Center,
  Loader,
  Alert,
  Box,
} from '@mantine/core'
import { authService } from '@/services/auth'

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

function Signup() {
  const navigate = useNavigate()

  const [orgName, setOrgName] = useState('')
  const [userName, setUserName] = useState('')
  const [loading, setLoading] = useState(false)
  const [loadingUser, setLoadingUser] = useState(true)
  const [currentUser, setCurrentUser] = useState(null)
  const [error, setError] = useState(null)
  const [loginError, setLoginError] = useState(null)

  useEffect(() => {
    const storedError = localStorage.getItem('login_error')
    if (storedError) {
      setLoginError(getLoginErrorMessage(storedError))
      localStorage.removeItem('login_error')
    }

    const fetchUser = async () => {
      try {
        const user = await authService.getCurrentUser()
        setCurrentUser(user)
      } catch {
        // No valid session — user may not have completed auth yet
        setCurrentUser(null)
      } finally {
        setLoadingUser(false)
      }
    }

    fetchUser()
  }, [])

  const isAlreadyRegistered =
    currentUser?.role === 'admin' || currentUser?.role === 'standard'

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError(null)
    setLoading(true)

    try {
      await authService.signup(orgName, userName || undefined)
      navigate('/')
    } catch (err) {
      setError(err.response?.data?.message || 'Failed to set up organization. Please try again.')
    } finally {
      setLoading(false)
    }
  }

  if (loadingUser) {
    return (
      <Center style={{ height: '100vh' }}>
        <Loader size="lg" />
      </Center>
    )
  }

  return (
    <Container size={420} my={40}>
      <Paper withBorder shadow="md" p={30} mt={30} radius="md">
        <Stack align="center" mb="lg">
          <Title order={2} ta="center">
            Welcome to hoop.dev
          </Title>
          <Text size="sm" ta="center" c="dimmed">
            Before getting started, set a name for your organization.
          </Text>
        </Stack>

        {loginError && (
          <Alert color="red" mb="md">
            {loginError}
          </Alert>
        )}

        {error && (
          <Alert color="red" mb="md">
            {error}
          </Alert>
        )}

        <form onSubmit={handleSubmit}>
          <Stack>
            <TextInput
              placeholder="Dunder Mifflin Inc"
              value={orgName}
              onChange={(e) => setOrgName(e.target.value)}
              required
              disabled={isAlreadyRegistered}
            />

            {!currentUser?.name && (
              <TextInput
                label="Your name"
                placeholder="Michael Scott"
                value={userName}
                onChange={(e) => setUserName(e.target.value)}
                required
                disabled={isAlreadyRegistered}
              />
            )}

            <Box ta="center">
              <Button type="submit" loading={loading} disabled={isAlreadyRegistered}>
                Get started
              </Button>
            </Box>
          </Stack>
        </form>

        {isAlreadyRegistered && (
          <Alert color="yellow" mt="md">
            <Text size="xs" ta="center">
              {currentUser.role === 'admin'
                ? 'You are already registered as admin, please '
                : 'You are already registered, please '}
              <Anchor size="xs" onClick={() => navigate('/login')}>
                click here and sign in instead of signing up.
              </Anchor>
            </Text>
          </Alert>
        )}
      </Paper>
    </Container>
  )
}

export default Signup

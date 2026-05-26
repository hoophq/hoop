import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Box,
  SimpleGrid,
  Stack,
  Group,
  Flex,
  Title,
  Text,
} from '@mantine/core'
import { Lock } from 'lucide-react'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import Button from '@/components/Button'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { authService } from '@/services/auth'
import classes from './Setup.module.css'

const TRUSTED_BY = ['EBANX', 'RD Station', 'Dock', 'PicPay', 'Unico']

function passwordStrengthScore(pw) {
  let score = 0
  if (pw.length >= 8) score += 1
  if (pw.length >= 12) score += 1
  if (/[A-Z]/.test(pw) && /[a-z]/.test(pw)) score += 1
  if (/\d/.test(pw) && /[^\w\s]/.test(pw)) score += 1
  return Math.min(score, 4)
}

function strengthLabel(score) {
  return { 1: 'weak', 2: 'fair', 3: 'good', 4: 'strong' }[score] || ''
}

function LeftPanel() {
  return (
    <Box component="aside" className={classes.leftPanel}>
      <Box>
        <Flex align="center" gap="xs" mb="xl">
          <img
            src="/images/hoop-branding/SVG/hoop-symbol_white.svg"
            alt="hoop.dev"
            width={20}
          />
          <Text size="sm" fw={700} c="white">
            hoop.dev
          </Text>
        </Flex>

        <Text fz="xs" fw={700} tt="uppercase" c="gray.4" mb="md" className={classes.eyebrow}>
          Instance ready
        </Text>

        <Title order={1} mb="sm" c="white" className={classes.heroHeading}>
          The first account becomes the root admin.
        </Title>

        <Text size="sm" c="gray.6" className={classes.heroBody}>
          {'Once you sign up, the gateway starts intercepting connections. Everything below runs on your machine.'}
        </Text>
      </Box>

      <Box my="xl">
        <Text fz="xs" fw={700} tt="uppercase" c="gray.7" mb="sm" className={classes.eyebrow}>
          Trusted in production by
        </Text>

        <Flex wrap="wrap" align="center" gap="xs" className={classes.brandsDivider}>
          {TRUSTED_BY.map((name, i) => (
            <Group key={name} gap="xs" align="center">
              {i > 0 && <span className={classes.dot} />}
              <Text size="sm" fw={700} c="gray.5">
                {name}
              </Text>
            </Group>
          ))}
        </Flex>

        <Box component="figure" m={0}>
          <Box component="blockquote" className={classes.blockquote}>
            {'Zero setup for GDPR, SOC2, and PCI across our databases, Kubernetes clusters, and AWS accounts. We replaced our in-house tool in a week.'}
          </Box>
          <Flex component="figcaption" align="center" gap="xs" mt="sm">
            <Text component="span" size="xs" fw={700} c="white">
              Staff Engineer
            </Text>
            <Text component="span" size="xs" c="gray.6">·</Text>
            <Text component="span" size="xs" fw={500} c="gray.6">
              RD Station
            </Text>
          </Flex>
        </Box>
      </Box>

      <Flex align="center" gap="xs">
        <Text size="xs" ff="monospace" c="gray.7">
          localhost:8009
        </Text>
        <Box className={classes.statusDivider} />
        <Flex align="center" gap={6}>
          <Box className={classes.pulseDot} />
          <Text size="xs" fw={700} tt="uppercase" c="gray.6" className={classes.gatewayOnline}>
            Gateway online
          </Text>
        </Flex>
      </Flex>
    </Box>
  )
}

function Setup() {
  const navigate = useNavigate()
  const { setToken } = useAuthStore()
  const { setUser } = useUserStore()

  const [fullName, setFullName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [pwScore, setPwScore] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  useEffect(() => {
    if (useAuthStore.getState().isAuthenticated) {
      navigate('/', { replace: true })
    }
  }, [navigate])

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (loading) return

    setError(null)
    setLoading(true)
    try {
      const { token, user } = await authService.registerLocal(email, password, fullName)
      setToken(token)
      setUser(user)
      navigate('/', { replace: true })
    } catch (err) {
      setError(
        err.response?.data?.message || 'Something went wrong. Please try again.'
      )
    } finally {
      setLoading(false)
    }
  }

  return (
    <Box mih="100vh" bg="gray.1" p="xl" className={classes.shell}>
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg" w="100%" maw={1080}>
        <LeftPanel />

        <Box bg="white" p="xxlAlt" className={classes.rightPanel}>
          <Text fz="xs" fw={500} tt="uppercase" c="dimmed" mb="xl" className={classes.eyebrow}>
            Create admin
          </Text>

          <Title order={2} mb={4} className={classes.formHeading}>
            Set up your instance
          </Title>
          <Text size="sm" c="dimmed" mb="lg">
            Takes less than a minute. Add a connection right after.
          </Text>

          <form onSubmit={handleSubmit}>
            <Stack gap="md">
              <TextInput
                label="Full name"
                placeholder="Jane Cooper"
                value={fullName}
                onChange={(e) => setFullName(e.currentTarget.value)}
                required
                type="text"
              />

              <TextInput
                label="Work email"
                placeholder="jane@company.com"
                value={email}
                onChange={(e) => setEmail(e.currentTarget.value)}
                required
                type="email"
              />

              <Box>
                <Flex justify="space-between" align="baseline" mb={4}>
                  <Text size="sm" fw={500} component="label" htmlFor="setup-password">
                    Password
                  </Text>
                  {pwScore > 0 && (
                    <Text size="xs" ff="monospace" c="dimmed">
                      {strengthLabel(pwScore)}
                    </Text>
                  )}
                </Flex>
                <PasswordInput
                  id="setup-password"
                  placeholder="At least 12 characters"
                  value={password}
                  onChange={(e) => {
                    const v = e.currentTarget.value
                    setPassword(v)
                    setPwScore(passwordStrengthScore(v))
                  }}
                  required
                  minLength={12}
                />
                <Flex gap={4} mt="xs">
                  {[0, 1, 2, 3].map((i) => (
                    <Box
                      key={i}
                      className={classes.strengthBar}
                      data-active={i < pwScore}
                    />
                  ))}
                </Flex>
              </Box>

              {error && (
                <Text size="xs" c="red">
                  {error}
                </Text>
              )}

              <Button type="submit" size="md" fullWidth loading={loading} disabled={loading}>
                {loading ? 'Creating account...' : 'Create admin account'}
              </Button>

              <Flex align="center" justify="center" gap="xs" mt="xs">
                <Lock size={11} color="var(--mantine-color-gray-6)" />
                <Text size="xs" c="dimmed">
                  Runs locally. Credentials never leave this machine.
                </Text>
              </Flex>
            </Stack>
          </form>
        </Box>
      </SimpleGrid>
    </Box>
  )
}

export default Setup

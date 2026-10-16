import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Group, Paper, Stack, Text, Title } from '@mantine/core'
import { Terminal } from 'lucide-react'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import { sidecarsService } from '@/services/sidecars'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import FinishInTerminal from './sections/FinishInTerminal'

const errorMessage = (err, fallback) => err?.response?.data?.message || fallback

function ErrorText({ children }) {
  return (
    <Text c="red" size="sm">
      {children}
    </Text>
  )
}

/**
 * One code's request. Keyed by the code, so a new code starts with fresh
 * state instead of resetting it inside the effect.
 */
function LoginDecision({ code }) {
  const navigate = useNavigate()
  const [login, setLogin] = useState(null)
  const [name, setName] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [deciding, setDeciding] = useState(null)
  // approved is the sidecar the approval created; the page then sends the
  // person back to the terminal to finish the setup.
  const [approved, setApproved] = useState(null)

  useEffect(() => {
    let current = true
    sidecarsService
      .getLogin(code)
      .then(({ data }) => {
        if (!current) return
        setLogin(data)
        setName(data.name)
      })
      .catch((err) => current && setError(errorMessage(err, 'This code was not found.')))
      .finally(() => current && setLoading(false))
    return () => {
      current = false
    }
  }, [code])

  const approve = async () => {
    setDeciding('approve')
    setError(null)
    try {
      const { data } = await sidecarsService.approveLogin(code, name.trim())
      useSidecarStore.getState().fetchSidecars()
      setApproved(data)
    } catch (err) {
      setError(errorMessage(err, 'Failed to approve the sidecar.'))
      setDeciding(null)
    }
  }

  const deny = async () => {
    setDeciding('deny')
    setError(null)
    try {
      await sidecarsService.denyLogin(code)
      showSnackbar({ level: 'info', text: 'Sidecar login denied.' })
      navigate('/sidecars')
    } catch (err) {
      setError(errorMessage(err, 'Failed to deny the sidecar login.'))
      setDeciding(null)
    }
  }

  if (approved) return <FinishInTerminal sidecar={approved} />
  if (loading) return <PageLoader h={200} />
  if (!login) return <ErrorText>{error}</ErrorText>

  const pending = login.status === 'pending'

  return (
    <Paper bg="gray.0" radius="xl" p="lg">
      <Stack gap="md">
        <Group gap="xs">
          <Terminal size={16} aria-hidden="true" />
          <Text size="sm" c="dimmed">
            Code in your terminal
          </Text>
          <Text fw={600}>{login.user_code}</Text>
        </Group>
        {pending ? (
          <>
            <Text size="sm" c="dimmed">
              Approve only if this code matches the one your terminal shows. The sidecar then joins your
              organization and connects on its own.
            </Text>
            <TextInput
              label="Sidecar name"
              description="Taken from the machine name. You can change it."
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
            />
            <Group>
              <Button loading={deciding === 'approve'} disabled={Boolean(deciding) || !name.trim()} onClick={approve}>
                Approve
              </Button>
              <Button variant="default" loading={deciding === 'deny'} disabled={Boolean(deciding)} onClick={deny}>
                Deny
              </Button>
            </Group>
          </>
        ) : (
          <Text size="sm">{`This login is ${login.status}. Start the sidecar again for a new code.`}</Text>
        )}
        {error && <ErrorText>{error}</ErrorText>}
      </Stack>
    </Paper>
  )
}

/**
 * Approves a sidecar that logged in by device code: `hoop start sidecar`
 * prints this page's URL with ?code=. Approving creates the sidecar in this
 * organization with the token the sidecar made for itself; the sidecar then
 * connects on its own. A one-shot page, so it calls the service directly
 * rather than through a store.
 */
export default function SidecarLogin() {
  const [searchParams, setSearchParams] = useSearchParams()
  const code = searchParams.get('code') || ''
  const [typedCode, setTypedCode] = useState('')

  return (
    <Stack gap="xl" maw={560}>
      <Stack gap="sm">
        <Title order={1}>Connect a Sidecar</Title>
        <Text size="lg" c="dimmed">
          A sidecar started with hoop start sidecar asks to join your organization.
        </Text>
      </Stack>

      {code ? (
        <LoginDecision key={code} code={code} />
      ) : (
        <Paper bg="gray.0" radius="xl" p="lg">
          <Stack gap="md">
            <TextInput
              label="Code"
              description="The code the sidecar printed in its terminal"
              placeholder="BCDF-GHJK"
              value={typedCode}
              onChange={(e) => setTypedCode(e.currentTarget.value)}
            />
            <Group>
              <Button disabled={!typedCode.trim()} onClick={() => setSearchParams({ code: typedCode.trim() })}>
                Continue
              </Button>
            </Group>
          </Stack>
        </Paper>
      )}
    </Stack>
  )
}

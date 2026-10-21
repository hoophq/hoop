import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Paper, Stack, Text, Title } from '@mantine/core'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import { useSidecarStore } from '@/stores/useSidecarStore'

const LOOPBACK_HOSTS = ['127.0.0.1', 'localhost', '[::1]']

// The sidecar's own callback, on this machine: the token goes to the process
// the person just started, never to another host. Anything else is refused.
function loopbackCallback(raw) {
  try {
    const url = new URL(raw)
    if (url.protocol !== 'http:' || !LOOPBACK_HOSTS.includes(url.hostname) || !url.port) return null
    if (url.pathname !== '/pair' || url.username || url.password) return null
    return url
  } catch {
    return null
  }
}

/**
 * Pairs a sidecar started with `hoop start sidecar`: the CLI opened this page
 * with its localhost callback and a one-time state. The person confirms, the
 * page creates the sidecar and hands its token to that callback, and the CLI
 * stores it and sends the browser on to the sidecar's page.
 *
 * Admin only, like every sidecar page. A person with no account is sent to
 * sign up and comes back here (goAfterAuth).
 */
export default function SidecarPairPage() {
  const [params] = useSearchParams()
  const createSidecar = useSidecarStore((s) => s.createSidecar)
  const callback = loopbackCallback(params.get('callback') ?? '')
  const state = params.get('state') ?? ''
  const [name, setName] = useState(params.get('name') ?? '')
  const [pairing, setPairing] = useState(false)
  const [error, setError] = useState(null)

  const pair = async (e) => {
    e.preventDefault()
    setPairing(true)
    setError(null)
    try {
      const created = await createSidecar({ name: name.trim() })
      const back = new URL(callback)
      back.searchParams.set('state', state)
      back.searchParams.set('token', created.token)
      back.searchParams.set('id', created.id)
      back.searchParams.set('name', created.name)
      window.location.href = back.toString()
    } catch (err) {
      setError(err.response?.data?.message || 'Could not create the sidecar.')
      setPairing(false)
    }
  }

  if (!callback || !state) {
    return (
      <Alert color="red" variant="light" icon={<TriangleAlert size={16} />} title="This pairing link is not valid">
        <Text size="sm">Start the pairing again from the sidecar with `hoop start sidecar`.</Text>
      </Alert>
    )
  }

  return (
    <Paper withBorder radius="md" p="lg" maw={560}>
      <form onSubmit={pair}>
        <Stack>
          <Title order={2}>Pair this sidecar</Title>
          <Text size="sm" c="dimmed">
            {
              'The sidecar you started on this computer asks to connect to Hoop. Pairing creates it here and sends its token back to it, so it starts with this account from now on.'
            }
          </Text>
          <TextInput
            label="Sidecar name"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            required
            disabled={pairing}
          />
          {error && (
            <Text size="sm" c="red">
              {error}
            </Text>
          )}
          <Button type="submit" loading={pairing} disabled={!name.trim()}>
            Pair
          </Button>
        </Stack>
      </form>
    </Paper>
  )
}

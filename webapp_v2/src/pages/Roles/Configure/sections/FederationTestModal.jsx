import { useState } from 'react'
import { Group, Loader, Paper, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { Check, CircleX, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Code from '@/components/Code'
import Modal from '@/components/Modal'
import TextInput from '@/components/TextInput'
import { useUserStore } from '@/stores/useUserStore'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'

const EMAIL_RE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/

function ResultRow({ label, value }) {
  return (
    <Stack gap={2}>
      <Text size="xs" c="dimmed" tt="uppercase">{label}</Text>
      <Text size="sm" ff="monospace">{value || '-'}</Text>
    </Stack>
  )
}

function TestSuccess({ result, email, ttlSeconds }) {
  return (
    <Stack gap="md">
      <Group gap="xs" wrap="nowrap">
        <ThemeIcon size="sm" variant="light" color="green">
          <Check size={12} />
        </ThemeIcon>
        <Text fw={600}>{'Session would start as ' + email}</Text>
      </Group>
      <Paper withBorder p="md">
        <Stack gap="sm">
          <ResultRow label="Resolved principal" value={result.resolved_principal} />
          <ResultRow label="Impersonated via" value={result.admin_principal} />
          <ResultRow label="Token TTL" value={Math.floor((ttlSeconds || 3600) / 60) + ' minutes'} />
          <ResultRow label="Env vars emitted" value={(result.env_var_keys || []).join(', ')} />
        </Stack>
      </Paper>
    </Stack>
  )
}

function TestFailure({ result }) {
  const detail = result?.error || result?.probe_output || result?.message || 'No details returned.'
  return (
    <Alert color="red" variant="light" icon={<CircleX size={16} />} title="Test failed">
      <Stack gap="xs">
        {result?.probe_status && (
          <Text size="xs">{'Probe status: ' + result.probe_status}</Text>
        )}
        <Code block w="100%" mah={192}>{detail}</Code>
      </Stack>
    </Alert>
  )
}

export default function FederationTestModal({ opened, onClose }) {
  const userEmail = useUserStore((s) => s.user?.email)
  const ttlSeconds = useConfigureRoleStore((s) => s.federation.form.token_ttl_seconds)
  const test = useConfigureRoleStore((s) => s.federationTest)
  const runFederationTest = useConfigureRoleStore((s) => s.runFederationTest)
  const resetFederationTest = useConfigureRoleStore((s) => s.resetFederationTest)
  const [email, setEmail] = useState(userEmail || '')

  const loading = test.status === 'loading'
  const success = test.status === 'success'

  const close = () => {
    resetFederationTest()
    onClose()
  }

  return (
    <Modal
      opened={opened}
      onClose={close}
      withCloseButton={!loading}
      title={<Title order={3}>Test as user</Title>}
      size="lg"
      centered
    >
      <Stack gap="lg">
        <Text size="sm" c="dimmed">
          Run the federation hook as a specific Hoop user and inspect what the session would receive.
        </Text>

        {success ? (
          <TestSuccess result={test.result} email={email} ttlSeconds={ttlSeconds} />
        ) : (
          <>
            <TextInput
              label="Hoop user"
              type="email"
              placeholder="user@example.com"
              value={email}
              onChange={(e) => setEmail(e.currentTarget.value)}
              required
            />
            <Alert color="blue" variant="light" icon={<Info size={16} />}>
              This is a dry run. No session is opened, no audit record is created.
            </Alert>
            {loading && (
              <Group gap="xs">
                <Loader size="xs" />
                <Text size="sm" c="dimmed">Running test…</Text>
              </Group>
            )}
            {test.status === 'error' && <TestFailure result={test.result} />}
          </>
        )}

        <Group justify="flex-end">
          {success ? (
            <Button onClick={close}>Done</Button>
          ) : (
            <>
              <Button variant="default" onClick={close} disabled={loading}>Cancel</Button>
              <Button
                onClick={() => runFederationTest(email)}
                disabled={!EMAIL_RE.test(email) || loading}
              >
                Run test
              </Button>
            </>
          )}
        </Group>
      </Stack>
    </Modal>
  )
}

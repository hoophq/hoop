import { useMemo, useState } from 'react'
import { Stack, Title, Text } from '@mantine/core'
import Radio from '@/components/Radio'
import PredefinedFields from './shared/PredefinedFields'
import AgentSelector from './shared/AgentSelector'
import { useConfigureRoleStore } from '../../store'

// React-specific field schema. SSH's auth-method radio drives which
// field is rendered and which is required — neither is encodable in
// the JSON catalog (the gateway's metadata marks PASS and
// AUTHORIZED_SERVER_KEYS as optional individually because the user
// must supply exactly one), so the shape lives here next to the
// renderer that owns the rule.
const SSH_FIELDS = [
  { key: 'host', label: 'Host', required: true },
  { key: 'port', label: 'Port', required: false },
  { key: 'user', label: 'User', required: true },
  { key: 'pass', label: 'Pass', required: true },
  {
    key: 'authorized_server_keys',
    label: 'Private Key',
    required: true,
    placeholder: 'Enter your private key',
    type: 'textarea',
  },
]

// Every credential key the proxy mode may persist. Local mode clears
// them all on save — the agent runs the shell on its own host and the
// gateway stores an empty secret map (mirrors CLJS process_form.cljs,
// which submits `credentials {}` for local SSH).
const SSH_SECRET_KEYS = [
  'envvar:HOST',
  'envvar:PORT',
  'envvar:USER',
  'envvar:PASS',
  'envvar:AUTHORIZED_SERVER_KEYS',
]

// SSH credentials editor used by application/ssh and application/ssh-local.
//
// In proxy mode the connection accepts either password or private-key
// authentication. We derive the active method from whichever secret key
// the connection currently has set (envvar:PASS for password,
// envvar:AUTHORIZED_SERVER_KEYS for key) and let the admin switch
// between them — switching just hides the inactive field; clearing the
// opposite key is the save handler's responsibility (see store.save).
function deriveAuthMethod(connection) {
  const secrets = connection?.secret || {}
  if ('envvar:AUTHORIZED_SERVER_KEYS' in secrets) return 'key'
  return 'password'
}

export default function SshRenderer({ connection, availableSources, forceNewState, hideRoleInfo }) {
  const initialMethod = useMemo(() => deriveAuthMethod(connection), [connection])
  const [authMethod, setAuthMethod] = useState(initialMethod)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)
  const draftSubtype = useConfigureRoleStore((s) => s.drafts.subtype)

  // Proxy ("ssh") vs local ("ssh-local") lives in the subtype draft so
  // CredentialsTab (which hosts the connection-method cards outside this
  // renderer) and buildDraftsPatch see the same state. Mirrors the CLJS
  // edit page, which normalizes ssh-local to ssh + connection-type
  // "local" and re-derives the wire subtype on save.
  const mode = draftSubtype === 'ssh-local' ? 'local' : 'proxy'
  const isLocal = mode === 'local'

  const fields = SSH_FIELDS.filter((f) => {
    if (authMethod === 'password') return f.key !== 'authorized_server_keys'
    return f.key !== 'pass'
  })

  // Switching auth method: stage a delete on the now-unused key so it
  // gets removed when the form is saved. If the user switches back,
  // unstage. This mirrors the CLJS save handler's `case auth-method`
  // logic without needing a per-page mutex with the global save.
  const handleAuthChange = (next) => {
    setAuthMethod(next)
    const secrets = connection?.secret || {}
    const passKey = 'envvar:PASS'
    const keyKey = 'envvar:AUTHORIZED_SERVER_KEYS'
    if (next === 'password') {
      cancelSecretChange(passKey)
      if (keyKey in secrets) deleteSecret(keyKey)
    } else {
      cancelSecretChange(keyKey)
      if (passKey in secrets) deleteSecret(passKey)
    }
  }

  // Switching connection type: local clears every persisted SSH
  // credential on the wire (and drops unsaved typing on the rest);
  // back to proxy restores whatever the connection still stores, then
  // re-applies the auth-method exclusivity rule so the inactive
  // credential stays staged for delete.
  const handleModeChange = (next) => {
    if (next === mode) return
    const secrets = connection?.secret || {}
    if (next === 'local') {
      setDraft({ subtype: 'ssh-local' })
      for (const key of SSH_SECRET_KEYS) {
        if (key in secrets) deleteSecret(key)
        else cancelSecretChange(key)
      }
    } else {
      setDraft({ subtype: 'ssh' })
      for (const key of SSH_SECRET_KEYS) cancelSecretChange(key)
      const inactive =
        authMethod === 'password' ? 'envvar:AUTHORIZED_SERVER_KEYS' : 'envvar:PASS'
      if (inactive in secrets) deleteSecret(inactive)
    }
  }

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={4}>SSH Configuration</Title>
        <Text size="sm" c="dimmed">
          Provide SSH information to set up your connection.
        </Text>
      </Stack>

      <Stack gap="xs">
        <Title order={5} fw={500}>Connection Type</Title>
        <Radio.Group
          value={mode}
          onChange={handleModeChange}
          name="ssh-connection-type"
        >
          <Stack gap="xs" mt="xs">
            <Radio
              value="proxy"
              label="Proxy to a remote host"
              description="The agent authenticates to a remote SSH server and forwards the session. Configure the target host and credentials below."
            />
            <Radio
              value="local"
              label="Local (run on the agent host)"
              description="The agent runs the shell or command directly on the machine where it is deployed. No target host or credentials are required."
            />
          </Stack>
        </Radio.Group>
      </Stack>

      {!isLocal && (
        <>
          <Stack gap="xs">
            <Title order={5} fw={500}>Authentication Method</Title>
            <Radio.Group
              value={authMethod}
              onChange={handleAuthChange}
              name="ssh-auth-method"
            >
              <Stack gap="xs" mt="xs">
                <Radio value="password" label="Username & Password" />
                <Radio value="key" label="Private Key Authentication" />
              </Stack>
            </Radio.Group>
          </Stack>

          <PredefinedFields
            connection={connection}
            fields={fields}
            availableSources={availableSources}
            forceNewState={forceNewState}
            hideRoleInfo={hideRoleInfo}
          />
        </>
      )}

      <AgentSelector />
    </Stack>
  )
}

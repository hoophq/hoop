import { useMemo, useState } from 'react'
import { Stack, Radio, Title, Text } from '@mantine/core'
import PredefinedFieldsCredentials from './PredefinedFieldsCredentials'
import { CATALOG_FIELDS } from '../utils/credentialsSchema'
import { useConfigureRoleStore } from '../store'

// SSH credentials editor used by application/ssh, application/git and
// application/github connections.
//
// The connection accepts either password or private-key authentication.
// We derive the active method from whichever secret key the connection
// currently has set (envvar:PASS for password, envvar:AUTHORIZED_SERVER_KEYS
// for key) and let the admin switch between them — switching just hides
// the inactive field; clearing the opposite key is the save handler's
// responsibility (see store.save).
function deriveAuthMethod(connection) {
  const secrets = connection?.secret || {}
  if ('envvar:AUTHORIZED_SERVER_KEYS' in secrets) return 'key'
  return 'password'
}

export default function SSHCredentials({ connection, isAdmin }) {
  const initialMethod = useMemo(() => deriveAuthMethod(connection), [connection])
  const [authMethod, setAuthMethod] = useState(initialMethod)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  const allFields = CATALOG_FIELDS.ssh || []
  const fields = allFields.filter((f) => {
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

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={4}>SSH Configuration</Title>
        <Text size="sm" c="dimmed">
          Provide SSH information to set up your connection.
        </Text>
      </Stack>

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

      <PredefinedFieldsCredentials
        connection={connection}
        fields={fields}
        isAdmin={isAdmin}
      />
    </Stack>
  )
}

import { Stack } from '@mantine/core'
import { CredentialField } from '../components/CredentialField'
import { buildSshCommand, getHostname } from '../helpers'

export function SshCredentials({ credentials }) {
  return (
    <Stack gap="md">
      <CredentialField label="Host" value={getHostname()} />
      <CredentialField label="Username" value={credentials.username} />
      <CredentialField label="Password" value={credentials.password} />
      <CredentialField label="Port" value={credentials.port} />
    </Stack>
  )
}

// Built client-side rather than taken from the API's `command` field: that
// field inverts user and password relative to the username/password it returns
// in the same payload (gateway/api/connections/connection_credentials.go).
export function SshCommand({ credentials }) {
  return (
    <Stack gap="md">
      <CredentialField label="Command" value={buildSshCommand(credentials)} />
    </Stack>
  )
}

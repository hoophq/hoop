import { Stack, Text } from '@mantine/core'
import { CredentialField } from '../components/CredentialField'
import { buildPostgresConnectionString, getHostname } from '../helpers'

export function PostgresCredentials({ credentials }) {
  return (
    <Stack gap="md">
      <CredentialField label="Database Name" value={credentials.database_name} />
      <CredentialField label="Host" value={getHostname()} />
      <CredentialField label="Username" value={credentials.username} />
      <CredentialField label="Password" value={credentials.password} />
      <CredentialField label="Port" value={credentials.port} />
    </Stack>
  )
}

export function PostgresConnectionUri({ credentials }) {
  return (
    <Stack gap="md">
      <CredentialField
        label="Connection String"
        value={buildPostgresConnectionString(credentials)}
      />
      <Text fz="sm" c="dimmed">
        Works with DBeaver, DataGrip and most PostgreSQL clients
      </Text>
    </Stack>
  )
}

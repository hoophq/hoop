import { Stack } from '@mantine/core'
import { CredentialField } from '../components/CredentialField'
import { buildAwsSsmCommand } from '../helpers'

// The CLJS view carried a hardcoded "valid for 30 minutes starting now"
// callout that ignored expire_at and was simply wrong for any other duration.
// Dropped: the panel header already shows the real countdown.
export function AwsSsmCredentials({ credentials }) {
  return (
    <Stack gap="md">
      <CredentialField label="Command" value={buildAwsSsmCommand(credentials)} />
    </Stack>
  )
}

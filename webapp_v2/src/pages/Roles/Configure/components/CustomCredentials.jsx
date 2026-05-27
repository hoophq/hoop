import { useState } from 'react'
import { Stack, Title, Text, Button, Group, Alert } from '@mantine/core'
import { Plus, Info } from 'lucide-react'
import TextInput from '@/components/TextInput'
import SecretField from './SecretField'
import { decodeSecretValue, encodeSecretValue, isSecretReference } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Free-form credentials editor for custom connections without a known
// subtype. Each row is one envvar; the user can add new pairs and
// delete existing ones.
//
// The list of existing envvars comes from the connection payload (keys
// only — values are stripped server-side). New rows live in local
// state until the user types both a key and a value, at which point we
// commit them to the store's stagedSecrets map under "new" action.
export default function CustomCredentials({ connection, isAdmin }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  const [draftKey, setDraftKey] = useState('')
  const [draftValue, setDraftValue] = useState('')

  const currentSecrets = connection.secret || {}
  // Only show envvar:* keys here. filesystem:* belongs to the
  // configuration-files concern which we don't ship in this iteration.
  const envvarKeys = Object.keys(currentSecrets).filter((k) => k.startsWith('envvar:'))

  // Plus any newly-staged keys the user hasn't saved yet, so they show
  // up in the list immediately after Add.
  const stagedNewKeys = Object.entries(stagedSecrets)
    .filter(([k, change]) => change.action === 'new' && !envvarKeys.includes(k))
    .map(([k]) => k)

  const allKeys = [...envvarKeys, ...stagedNewKeys]

  const handleAdd = () => {
    const k = draftKey.trim()
    if (!k) return
    const fullKey = k.startsWith('envvar:') ? k : 'envvar:' + k.toUpperCase()
    replaceSecret(fullKey, encodeSecretValue(draftValue))
    setDraftKey('')
    setDraftValue('')
  }

  return (
    <Stack gap="lg">
      <Stack gap="xs">
        <Title order={4}>Environment variables</Title>
        <Text size="sm" c="dimmed">
          Define environment variables exposed at runtime to this
          connection. Values are write-only.
        </Text>
      </Stack>

      <Stack gap="lg">
        {allKeys.length === 0 && (
          <Alert variant="light" color="gray" icon={<Info size={16} />}>
            No environment variables set. Add one below.
          </Alert>
        )}
        {allKeys.map((envKey) => {
          const encodedValue = currentSecrets[envKey]
          const staged = stagedSecrets[envKey]
          const isExisting =
            envKey in currentSecrets &&
            (encodedValue !== '' || connection.secrets_updated_at != null)
          const isReference = isSecretReference(encodedValue)
          const referenceText = isReference ? decodeSecretValue(encodedValue) : ''
          const displayLabel = envKey.startsWith('envvar:')
            ? envKey.slice('envvar:'.length)
            : envKey
          return (
            <SecretField
              key={envKey}
              label={displayLabel}
              isExisting={isExisting}
              isReference={isReference}
              referenceText={referenceText}
              allowDelete
              stagedAction={staged?.action}
              stagedValue={staged?.value ? decodeSecretValue(staged.value) : ''}
              secretsUpdatedAt={connection.secrets_updated_at}
              onReplace={(plain) =>
                isAdmin && replaceSecret(envKey, encodeSecretValue(plain))
              }
              onChangeStaged={(plain) =>
                isAdmin && replaceSecret(envKey, encodeSecretValue(plain))
              }
              onCancel={() => cancelSecretChange(envKey)}
              onDelete={() => isAdmin && deleteSecret(envKey)}
              onRemove={() => cancelSecretChange(envKey)}
            />
          )
        })}
      </Stack>

      <Stack gap="xs">
        <Title order={5} fw={500}>Add a new variable</Title>
        <Group gap="sm" align="flex-end" wrap="nowrap">
          <TextInput
            label="Key"
            placeholder="e.g. API_TOKEN"
            value={draftKey}
            onChange={(e) => setDraftKey(e.currentTarget.value)}
            flex={1}
            disabled={!isAdmin}
          />
          <TextInput
            label="Value"
            placeholder="Enter value"
            value={draftValue}
            onChange={(e) => setDraftValue(e.currentTarget.value)}
            flex={1}
            disabled={!isAdmin}
          />
          <Button
            leftSection={<Plus size={14} />}
            onClick={handleAdd}
            disabled={!draftKey.trim() || !isAdmin}
          >
            Add
          </Button>
        </Group>
      </Stack>
    </Stack>
  )
}

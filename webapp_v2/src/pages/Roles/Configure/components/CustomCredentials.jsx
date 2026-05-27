import { useState } from 'react'
import { Stack, Title, Text, Button, Grid, ActionIcon } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import { decodeSecretValue, encodeSecretValue } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'
import ConfigurationFilesSection from './ConfigurationFilesSection'
import CommandArgsInput from './CommandArgsInput'
import AgentSelector from './AgentSelector'
import ResourceSubtypeOverride from './ResourceSubtypeOverride'

// Free-form credentials editor for custom connections. Backend round-
// trips values plaintext for free-form custom, so the value field
// shows the actual current value behind PasswordInput's reveal toggle.
// Renames commit on blur — the row stages a delete on the old key
// plus a replace on the new one so mergeSecrets persists both halves
// in a single PATCH.

function EnvvarRow({ envKey, value, onCommitKey, onValueChange, onRemove }) {
  const initialName = envKey.startsWith('envvar:')
    ? envKey.slice('envvar:'.length)
    : envKey
  const [draftName, setDraftName] = useState(initialName)

  return (
    <Grid gutter="md" align="flex-end">
      <Grid.Col span={5}>
        <TextInput
          label="Key"
          value={draftName}
          onChange={(e) => setDraftName(e.currentTarget.value)}
          onBlur={() => {
            const trimmed = draftName.trim()
            if (!trimmed || trimmed === initialName) return
            onCommitKey(trimmed)
          }}
          placeholder="e.g. API_KEY"
        />
      </Grid.Col>
      <Grid.Col span={6}>
        <PasswordInput
          label="Value"
          value={value}
          onChange={(e) => onValueChange(e.currentTarget.value)}
          placeholder="Enter value"
        />
      </Grid.Col>
      <Grid.Col span={1}>
        <ActionIcon
          variant="subtle"
          color="red"
          size="lg"
          onClick={onRemove}
          aria-label={'Remove ' + initialName}
        >
          <Trash2 size={16} />
        </ActionIcon>
      </Grid.Col>
    </Grid>
  )
}

export default function CustomCredentials({ connection }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  const currentSecrets = connection.secret || {}
  const stagedDeletedKeys = new Set(
    Object.entries(stagedSecrets)
      .filter(([, change]) => change.action === 'delete')
      .map(([k]) => k),
  )
  const existingKeys = Object.keys(currentSecrets)
    .filter((k) => k.startsWith('envvar:'))
    .filter((k) => !stagedDeletedKeys.has(k))
  const stagedNewKeys = Object.entries(stagedSecrets)
    .filter(
      ([k, change]) =>
        change.action === 'new' &&
        k.startsWith('envvar:') &&
        !existingKeys.includes(k),
    )
    .map(([k]) => k)
  const allKeys = [...existingKeys, ...stagedNewKeys]

  const addEmptyRow = () => {
    let i = 1
    while (allKeys.includes(`envvar:NEW_KEY_${i}`)) i += 1
    replaceSecret(`envvar:NEW_KEY_${i}`, '')
  }

  const renameKey = (envKey, newName, currentValue) => {
    const nextKey = newName.startsWith('envvar:')
      ? newName
      : 'envvar:' + newName.toUpperCase()
    if (nextKey === envKey) return
    if (envKey in currentSecrets) deleteSecret(envKey)
    else cancelSecretChange(envKey)
    replaceSecret(nextKey, encodeSecretValue(currentValue))
  }

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={4}>Environment variables</Title>
        <Text size="sm" c="dimmed">
          Include environment variables to be used in your resource role.
        </Text>
      </Stack>

      <Stack gap="md">
        {allKeys.map((envKey) => {
          const staged = stagedSecrets[envKey]
          const isExisting = envKey in currentSecrets
          const stagedPlain = staged?.value
            ? decodeSecretValue(staged.value)
            : null
          const persistedPlain = currentSecrets[envKey]
            ? decodeSecretValue(currentSecrets[envKey])
            : ''
          const value = stagedPlain != null ? stagedPlain : persistedPlain
          return (
            <EnvvarRow
              key={envKey}
              envKey={envKey}
              value={value}
              onCommitKey={(newName) => renameKey(envKey, newName, value)}
              onValueChange={(plain) =>
                replaceSecret(envKey, encodeSecretValue(plain))
              }
              onRemove={() => {
                if (isExisting) deleteSecret(envKey)
                else cancelSecretChange(envKey)
              }}
            />
          )
        })}
        <Button
          variant="light"
          leftSection={<Plus size={14} />}
          w="fit-content"
          onClick={addEmptyRow}
        >
          Add key/value
        </Button>
      </Stack>

      <ConfigurationFilesSection connection={connection} />
      <CommandArgsInput />
      <ResourceSubtypeOverride />
      <AgentSelector />
    </Stack>
  )
}

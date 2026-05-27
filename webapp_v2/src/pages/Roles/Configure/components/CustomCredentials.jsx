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

// Free-form credentials editor for custom connections. Matches the
// CLJS create UI: each envvar is a key + value pair. Backend round-
// trips values plaintext for free-form custom (see secrets.go::
// isFreeFormCustom), so the value field shows the actual current
// value behind PasswordInput's reveal toggle — same UX a password
// manager offers.

function envvarRow({
  envKey,
  encodedValue,
  staged,
  isAdmin,
  isExisting,
  onKeyChange,
  onValueChange,
  onRemove,
}) {
  const displayName = envKey.startsWith('envvar:')
    ? envKey.slice('envvar:'.length)
    : envKey
  // Free-form values come back plaintext, but the user may also have
  // staged a fresh value during this session — staged wins so what
  // you type is what you see.
  const stagedPlain = staged?.value ? decodeSecretValue(staged.value) : null
  const persistedPlain = encodedValue ? decodeSecretValue(encodedValue) : ''
  const value = stagedPlain != null ? stagedPlain : persistedPlain
  return (
    <Grid key={envKey} gutter="md" align="flex-end">
      <Grid.Col span={5}>
        <TextInput
          label="Key"
          value={displayName}
          onChange={(e) => onKeyChange(e.currentTarget.value)}
          disabled={isExisting || !isAdmin}
          placeholder="e.g. API_KEY"
        />
      </Grid.Col>
      <Grid.Col span={6}>
        <PasswordInput
          label="Value"
          value={value}
          onChange={(e) => onValueChange(e.currentTarget.value)}
          placeholder="Enter value"
          disabled={!isAdmin}
        />
      </Grid.Col>
      <Grid.Col span={1}>
        <ActionIcon
          variant="subtle"
          color="red"
          size="lg"
          onClick={onRemove}
          aria-label={'Remove ' + displayName}
          disabled={!isAdmin}
        >
          <Trash2 size={16} />
        </ActionIcon>
      </Grid.Col>
    </Grid>
  )
}

export default function CustomCredentials({ connection, isAdmin }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  const currentSecrets = connection.secret || {}
  const existingKeys = Object.keys(currentSecrets).filter((k) =>
    k.startsWith('envvar:'),
  )
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
    const key = `envvar:NEW_KEY_${i}`
    replaceSecret(key, '')
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
          return envvarRow({
            envKey,
            encodedValue: currentSecrets[envKey],
            staged,
            isAdmin,
            isExisting,
            onKeyChange: (newName) => {
              // Only new envvars can be renamed; existing keys are
              // disabled. Stage a replace on the new key + cancel the
              // old placeholder so save sends the right thing.
              const nextKey = newName.startsWith('envvar:')
                ? newName
                : 'envvar:' + newName.toUpperCase()
              if (nextKey === envKey) return
              cancelSecretChange(envKey)
              replaceSecret(nextKey, staged?.value || '')
            },
            onValueChange: (plain) => {
              if (!isAdmin) return
              replaceSecret(envKey, encodeSecretValue(plain))
            },
            onRemove: () => {
              if (!isAdmin) return
              if (isExisting) deleteSecret(envKey)
              else cancelSecretChange(envKey)
            },
          })
        })}
        <Button
          variant="light"
          leftSection={<Plus size={14} />}
          w="fit-content"
          onClick={addEmptyRow}
          disabled={!isAdmin}
        >
          Add key/value
        </Button>
      </Stack>

      <ConfigurationFilesSection connection={connection} isAdmin={isAdmin} />
      <CommandArgsInput />
      <ResourceSubtypeOverride />
      <AgentSelector />
    </Stack>
  )
}

import { useEffect, useState } from 'react'
import { Stack, Title, Text, Grid } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import { decodeSecretValue, encodeSecretValue, PLACEHOLDER_KEY_RE } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'
import ConfigurationFilesSection from './ConfigurationFilesSection'
import CommandArgsInput from './CommandArgsInput'
import AgentSelector from './AgentSelector'
import ResourceSubtypeOverride from './ResourceSubtypeOverride'

// Free-form credentials editor for custom connections. Values round-
// trip plaintext from the backend; rename commits on blur and is
// translated into delete-old + replace-new at save time so the row's
// rendered position stays put. The list never shrinks past one empty
// row — auto-adds a placeholder if everything was removed.

function EnvvarRow({ rowKey, displayName, value, onCommitKey, onValueChange, onRemove }) {
  const [draftName, setDraftName] = useState(displayName)

  // Keep the local draft in sync when the displayed name changes from
  // outside (e.g. another component triggered a rename for this key).
  useEffect(() => {
    setDraftName(displayName)
  }, [displayName])

  return (
    <Grid gutter="md" align="flex-end" key={rowKey}>
      <Grid.Col span={5}>
        <TextInput
          label="Key"
          value={draftName}
          onChange={(e) => setDraftName(e.currentTarget.value)}
          onBlur={() => {
            const trimmed = draftName.trim()
            if (!trimmed) return
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
          aria-label={'Remove ' + displayName}
        >
          <Trash2 size={16} />
        </ActionIcon>
      </Grid.Col>
    </Grid>
  )
}

export default function CustomCredentials({ connection }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const renames = useConfigureRoleStore((s) => s.renames)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const renameSecret = useConfigureRoleStore((s) => s.renameSecret)

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

  // Keep at least one row available so the section never collapses.
  // Matches CLJS behaviour (the legacy form always shows a blank input).
  useEffect(() => {
    if (allKeys.length === 0) {
      let i = 1
      const sentinel = `envvar:NEW_KEY_${i}`
      replaceSecret(sentinel, '')
    }
  }, [allKeys.length, replaceSecret])

  const addEmptyRow = () => {
    let i = 1
    while (allKeys.includes(`envvar:NEW_KEY_${i}`)) i += 1
    replaceSecret(`envvar:NEW_KEY_${i}`, '')
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
          const renamedTo = renames[envKey]
          const effectiveKey = renamedTo || envKey
          // Hide the auto-generated `NEW_KEY_N` sentinel from the user
          // so the empty-state row shows a truly blank Key input.
          const isPlaceholder = PLACEHOLDER_KEY_RE.test(effectiveKey)
          const displayName = isPlaceholder ? '' : effectiveKey.slice('envvar:'.length)
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
              rowKey={envKey}
              displayName={displayName}
              value={value}
              onCommitKey={(newName) => {
                const nextKey = newName.startsWith('envvar:')
                  ? newName
                  : 'envvar:' + newName.toUpperCase()
                renameSecret(envKey, nextKey)
              }}
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

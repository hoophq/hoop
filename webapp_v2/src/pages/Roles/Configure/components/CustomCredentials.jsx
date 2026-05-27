import { Stack, Title, Text, Button, Grid, ActionIcon } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import {
  decodeSecretValue,
  encodeSecretForSource,
  SOURCES,
} from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'
import ConfigurationFilesSection from './ConfigurationFilesSection'
import CommandArgsInput from './CommandArgsInput'
import AgentSelector from './AgentSelector'
import Select from '@/components/Select'
import { SOURCE_LABELS } from '../utils/secretsCodec'

// Free-form credentials editor for custom connections. Matches the
// CLJS create UI: each envvar is a key + value pair (value is a
// PasswordInput with a built-in reveal toggle), plus a single Add
// button below the list. Edit mode looks identical to create mode —
// existing keys are pre-listed but values come back blank from the
// gateway under the write-only contract; typing a new value stages a
// replace.

function envvarRow({
  envKey,
  isExisting,
  staged,
  isAdmin,
  source,
  availableSources,
  onKeyChange,
  onValueChange,
  onSourceChange,
  onRemove,
}) {
  const displayName = envKey.startsWith('envvar:')
    ? envKey.slice('envvar:'.length)
    : envKey
  const stagedPlain = staged?.value
    ? decodeSecretValue(staged.value).replace(
        /^(_aws:|_envjson:|_vaultkv1:|_vaultkv2:|_aws_iam_rds:)/,
        '',
      )
    : ''
  const placeholder = isExisting && !staged ? '••••' : 'Enter value'
  const showSourceSelector = availableSources && availableSources.length > 1
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
        <Stack gap={4}>
          <Text size="sm" fw={500}>Value</Text>
          <Grid gutter="xs" align="center">
            {showSourceSelector && (
              <Grid.Col span={5}>
                <Select
                  data={availableSources.map((s) => ({
                    value: s,
                    label: SOURCE_LABELS[s] || s,
                  }))}
                  value={source}
                  onChange={(v) => v && onSourceChange(v)}
                  allowDeselect={false}
                  size="sm"
                />
              </Grid.Col>
            )}
            <Grid.Col span={showSourceSelector ? 7 : 12}>
              <PasswordInput
                value={stagedPlain}
                onChange={(e) => onValueChange(e.currentTarget.value)}
                placeholder={placeholder}
                disabled={!isAdmin}
              />
            </Grid.Col>
          </Grid>
        </Stack>
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

export default function CustomCredentials({ connection, isAdmin, availableSources }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const fieldSources = useConfigureRoleStore((s) => s.fieldSources)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const setFieldSource = useConfigureRoleStore((s) => s.setFieldSource)

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
    // Find a fresh placeholder key the user will fill in.
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
          const source = fieldSources[envKey] || SOURCES.MANUAL
          return envvarRow({
            envKey,
            isExisting,
            staged,
            isAdmin,
            source,
            availableSources,
            onKeyChange: (newName) => {
              // Only new envvars can be renamed (existing keys are
              // disabled). Stage delete on the placeholder + replace on
              // the new key so save() does the right thing.
              const nextKey = newName.startsWith('envvar:')
                ? newName
                : 'envvar:' + newName.toUpperCase()
              if (nextKey === envKey) return
              cancelSecretChange(envKey)
              replaceSecret(
                nextKey,
                staged?.value || '',
              )
            },
            onValueChange: (plain) => {
              if (!isAdmin) return
              replaceSecret(envKey, encodeSecretForSource(plain, source))
            },
            onSourceChange: (s) => setFieldSource(envKey, s),
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
      <AgentSelector />
    </Stack>
  )
}

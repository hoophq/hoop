import { useState } from 'react'
import { Stack, Title, Text, Button, Grid, ActionIcon, Textarea } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import TextInput from '@/components/TextInput'
import { decodeSecretValue, encodeSecretValue } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Configuration files for a custom connection. Matches the CLJS create
// UI: each file is a Name input + a Content textarea + a Remove icon,
// with an Add button below. The underlying storage key is
// `filesystem:<NAME>` so renaming a file is just delete-old + insert-new
// — the same staged-secret primitives that envvar rows use. Names are
// edited under local state and committed on blur to keep focus stable.

function FileRow({ fsKey, content, onCommitName, onContentChange, onRemove }) {
  const initialName = fsKey.slice('filesystem:'.length)
  const [draftName, setDraftName] = useState(initialName)

  return (
    <Grid gutter="md" align="flex-start">
      <Grid.Col span={11}>
        <Stack gap="md">
          <TextInput
            label="Name"
            value={draftName}
            placeholder="e.g. kubeconfig"
            onChange={(e) => setDraftName(e.currentTarget.value)}
            onBlur={() => {
              const trimmed = draftName.trim()
              if (!trimmed || trimmed === initialName) return
              onCommitName(trimmed)
            }}
          />
          <Stack gap={4}>
            <Text size="sm" fw={500}>Content</Text>
            <Textarea
              autosize
              minRows={4}
              placeholder="Paste your file content here"
              value={content}
              onChange={(e) => onContentChange(e.currentTarget.value)}
            />
          </Stack>
        </Stack>
      </Grid.Col>
      <Grid.Col span={1}>
        <ActionIcon
          variant="subtle"
          color="red"
          size="lg"
          mt="xl"
          onClick={onRemove}
          aria-label={'Remove configuration file ' + initialName}
        >
          <Trash2 size={16} />
        </ActionIcon>
      </Grid.Col>
    </Grid>
  )
}

export default function ConfigurationFilesSection({ connection }) {
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
    .filter((k) => k.startsWith('filesystem:'))
    .filter((k) => !stagedDeletedKeys.has(k))
  const stagedNewKeys = Object.entries(stagedSecrets)
    .filter(
      ([k, change]) =>
        change.action === 'new' &&
        k.startsWith('filesystem:') &&
        !existingKeys.includes(k),
    )
    .map(([k]) => k)
  const allKeys = [...existingKeys, ...stagedNewKeys]

  const addEmptyRow = () => {
    let i = 1
    while (allKeys.includes(`filesystem:NEW_FILE_${i}`)) i += 1
    replaceSecret(`filesystem:NEW_FILE_${i}`, '')
  }

  const renameKey = (fsKey, newName, currentContent) => {
    const nextKey = `filesystem:${newName}`
    if (nextKey === fsKey) return
    const isExisting = fsKey in currentSecrets
    if (isExisting) deleteSecret(fsKey)
    else cancelSecretChange(fsKey)
    replaceSecret(nextKey, encodeSecretValue(currentContent))
  }

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={4}>Configuration files</Title>
        <Text size="sm" c="dimmed">
          Add values from your configuration file and use them as an
          environment variable in your resource role.
        </Text>
      </Stack>

      <Stack gap="md">
        {allKeys.map((fsKey) => {
          const staged = stagedSecrets[fsKey]
          const isExisting = fsKey in currentSecrets
          const stagedContent = staged?.value
            ? decodeSecretValue(staged.value)
            : null
          const persistedContent = currentSecrets[fsKey]
            ? decodeSecretValue(currentSecrets[fsKey])
            : ''
          const content = stagedContent != null ? stagedContent : persistedContent
          return (
            <FileRow
              key={fsKey}
              fsKey={fsKey}
              content={content}
              onCommitName={(newName) => renameKey(fsKey, newName, content)}
              onContentChange={(plain) =>
                replaceSecret(fsKey, encodeSecretValue(plain))
              }
              onRemove={() => {
                if (isExisting) deleteSecret(fsKey)
                else cancelSecretChange(fsKey)
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
          Add
        </Button>
      </Stack>
    </Stack>
  )
}

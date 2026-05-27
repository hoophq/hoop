import { useEffect, useState } from 'react'
import { Stack, Title, Text, Grid } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { decodeSecretValue, encodeSecretValue, PLACEHOLDER_KEY_RE } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Configuration files for a custom connection. Same pattern as
// CustomCredentials: rename commits on blur via the store's renames
// map so position stays put; an empty placeholder row is kept around
// so the section never collapses.

function FileRow({ rowKey, displayName, content, onCommitName, onContentChange, onRemove }) {
  const [draftName, setDraftName] = useState(displayName)

  useEffect(() => {
    setDraftName(displayName)
  }, [displayName])

  return (
    <Grid gutter="md" align="flex-start" key={rowKey}>
      <Grid.Col span={11}>
        <Stack gap="md">
          <TextInput
            label="Name"
            value={draftName}
            placeholder="e.g. kubeconfig"
            onChange={(e) => setDraftName(e.currentTarget.value)}
            onBlur={() => {
              const trimmed = draftName.trim()
              if (!trimmed) return
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
          aria-label={'Remove configuration file ' + displayName}
        >
          <Trash2 size={16} />
        </ActionIcon>
      </Grid.Col>
    </Grid>
  )
}

export default function ConfigurationFilesSection({ connection }) {
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

  useEffect(() => {
    if (allKeys.length === 0) {
      replaceSecret('filesystem:NEW_FILE_1', '')
    }
  }, [allKeys.length, replaceSecret])

  const addEmptyRow = () => {
    let i = 1
    while (allKeys.includes(`filesystem:NEW_FILE_${i}`)) i += 1
    replaceSecret(`filesystem:NEW_FILE_${i}`, '')
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
          const renamedTo = renames[fsKey]
          const effectiveKey = renamedTo || fsKey
          // Hide the auto-generated `NEW_FILE_N` sentinel from the user
          // so the empty-state row shows a truly blank Name input.
          const isPlaceholder = PLACEHOLDER_KEY_RE.test(effectiveKey)
          const displayName = isPlaceholder ? '' : effectiveKey.slice('filesystem:'.length)
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
              rowKey={fsKey}
              displayName={displayName}
              content={content}
              onCommitName={(newName) => {
                renameSecret(fsKey, `filesystem:${newName}`)
              }}
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

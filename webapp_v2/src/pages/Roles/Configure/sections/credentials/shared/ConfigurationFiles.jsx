import { useEffect, useState } from 'react'
import { Stack, Title, Text, Grid } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import SecretField from '../../../components/SecretField'
import {
  decodeSecretValue,
  encodeSecretValue,
  isValidPosixKey,
  PLACEHOLDER_KEY_RE,
} from '../../../utils/secretsCodec'
import { useConfigureRoleStore } from '../../../store'

// Configuration files for a custom connection. Same pattern as the
// environment variables section: rename commits on blur via the store's
// renames map so position stays put; an empty placeholder row is kept
// around so the section never collapses.

function FileRow({ rowKey, displayName, content, writeOnly, stagedAction, onCommitName, onContentChange, onCancelReplace, onRemove }) {
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
            onChange={(e) => {
              // CLJS uppercases per keystroke (configuration_inputs.cljs:39-58)
              // and enforces POSIX. Mirror both: reject invalid input,
              // uppercase the accepted value.
              const next = e.currentTarget.value.toUpperCase()
              if (isValidPosixKey(next)) setDraftName(next)
            }}
            onBlur={() => {
              const trimmed = draftName.trim()
              if (!trimmed) return
              onCommitName(trimmed)
            }}
          />
          {writeOnly ? (
            <SecretField
              label="Content"
              type="textarea"
              isExisting
              stagedAction={stagedAction}
              stagedValue={content}
              onReplace={onContentChange}
              onChangeStaged={onContentChange}
              onCancel={onCancelReplace}
            />
          ) : (
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
          )}
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

export default function ConfigurationFiles({ connection, hideRoleInfo }) {
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
          // ONLY when it's an actual UI placeholder (not persisted on
          // the connection). Legacy junk records with the same name
          // shape — created by an older version that didn't reject
          // unnamed placeholders on save — must still display so the
          // user can see and clean them up.
          const isPlaceholder = !isExisting && PLACEHOLDER_KEY_RE.test(effectiveKey)
          const displayName = isPlaceholder ? '' : effectiveKey.slice('filesystem:'.length)
          // If anything is staged for this row we honour it verbatim,
          // even when the value is an empty string — that's "user
          // explicitly cleared the input" and we don't want to fall back
          // to the persisted content (would resurrect stale data when
          // the auto-placeholder kicks in after a delete).
          const content = staged
            ? decodeSecretValue(staged.value || '')
            : currentSecrets[fsKey]
              ? decodeSecretValue(currentSecrets[fsKey])
              : ''
          const writeOnly = Boolean(hideRoleInfo) && isExisting
          return (
            <FileRow
              key={fsKey}
              rowKey={fsKey}
              displayName={displayName}
              content={content}
              writeOnly={writeOnly}
              stagedAction={staged?.action}
              onCommitName={(newName) => {
                renameSecret(fsKey, `filesystem:${newName}`)
              }}
              onContentChange={(plain) =>
                replaceSecret(fsKey, encodeSecretValue(plain))
              }
              onCancelReplace={() => cancelSecretChange(fsKey)}
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

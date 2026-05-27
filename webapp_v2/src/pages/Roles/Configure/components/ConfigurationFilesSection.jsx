import { Stack, Title, Text, Group, Button, ActionIcon } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import TextInput from '@/components/TextInput'
import SecretField from './SecretField'
import { decodeSecretValue, encodeSecretValue } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Configuration files for a custom connection. Each file becomes a
// `filesystem:<NAME>` envvar: the name is the filename that will be
// written, the content is the file body (write-only, so SecretField
// handles it). Add/Remove buttons mirror CLJS server/credentials-step.
export default function ConfigurationFilesSection({ connection, isAdmin }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  const currentSecrets = connection.secret || {}
  const existingKeys = Object.keys(currentSecrets).filter((k) =>
    k.startsWith('filesystem:'),
  )
  const stagedNewKeys = Object.entries(stagedSecrets)
    .filter(
      ([k, change]) =>
        change.action === 'new' &&
        k.startsWith('filesystem:') &&
        !existingKeys.includes(k),
    )
    .map(([k]) => k)
  const allKeys = [...existingKeys, ...stagedNewKeys]

  const handleAdd = () => {
    // Add a sentinel staged "new" entry with an empty key — the user
    // fills in the filename via the inline label edit. For simplicity
    // we prompt with a placeholder filename derived from a counter.
    let i = 1
    while (allKeys.includes(`filesystem:FILE_${i}`)) i += 1
    const newKey = `filesystem:FILE_${i}`
    replaceSecret(newKey, encodeSecretValue(''))
  }

  return (
    <Stack gap="xs">
      <Title order={5} fw={500}>Configuration files</Title>
      <Text size="sm" c="dimmed">
        {"Files written to the agent's filesystem at session time. Use the filename as the field label."}
      </Text>
      <Stack gap="md">
        {allKeys.map((fsKey) => {
          const encodedValue = currentSecrets[fsKey]
          const staged = stagedSecrets[fsKey]
          const filename = fsKey.slice('filesystem:'.length)
          const isExisting =
            fsKey in currentSecrets &&
            (encodedValue !== '' || connection.secrets_updated_at != null)
          return (
            <Stack key={fsKey} gap="xs">
              <TextInput
                label="Filename"
                value={filename}
                onChange={(e) => {
                  const next = `filesystem:${e.currentTarget.value}`
                  if (next === fsKey) return
                  // Rename: stage a delete on the old key + a new on the new key.
                  if (isExisting) deleteSecret(fsKey)
                  else cancelSecretChange(fsKey)
                  replaceSecret(
                    next,
                    staged?.value || encodeSecretValue(''),
                  )
                }}
                disabled={!isAdmin}
              />
              <SecretField
                label="Content"
                type="textarea"
                isExisting={isExisting}
                isReference={false}
                referenceText=""
                allowDelete
                stagedAction={staged?.action}
                stagedValue={staged?.value ? decodeSecretValue(staged.value) : ''}
                secretsUpdatedAt={connection.secrets_updated_at}
                onReplace={(plain) =>
                  isAdmin && replaceSecret(fsKey, encodeSecretValue(plain))
                }
                onChangeStaged={(plain) =>
                  isAdmin && replaceSecret(fsKey, encodeSecretValue(plain))
                }
                onCancel={() => cancelSecretChange(fsKey)}
                onDelete={() => isAdmin && deleteSecret(fsKey)}
                onRemove={() => cancelSecretChange(fsKey)}
              />
              <Group justify="flex-end">
                <ActionIcon
                  variant="subtle"
                  color="red"
                  size="sm"
                  onClick={() =>
                    isExisting ? deleteSecret(fsKey) : cancelSecretChange(fsKey)
                  }
                  aria-label={'Remove configuration file ' + filename}
                  disabled={!isAdmin}
                >
                  <Trash2 size={14} />
                </ActionIcon>
              </Group>
            </Stack>
          )
        })}
        <Button
          variant="light"
          leftSection={<Plus size={14} />}
          w="fit-content"
          onClick={handleAdd}
          disabled={!isAdmin}
        >
          Add configuration file
        </Button>
      </Stack>
    </Stack>
  )
}

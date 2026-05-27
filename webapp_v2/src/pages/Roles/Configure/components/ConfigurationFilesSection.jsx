import { Stack, Title, Text, Button, Grid, ActionIcon, Textarea } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import TextInput from '@/components/TextInput'
import { decodeSecretValue, encodeSecretValue } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Configuration files for a custom connection. Matches the CLJS create
// UI: each file is a Name input + a Content textarea + a Remove icon,
// with an Add button below. The underlying storage key is
// `filesystem:<NAME>` so new and existing files share the same
// envvar-style code paths (replace/delete/cancelSecretChange) — only
// the prefix differs from regular envvars.
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

  const addEmptyRow = () => {
    let i = 1
    while (allKeys.includes(`filesystem:NEW_FILE_${i}`)) i += 1
    const key = `filesystem:NEW_FILE_${i}`
    replaceSecret(key, '')
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
          const filename = fsKey.slice('filesystem:'.length)
          const stagedContent = staged?.value ? decodeSecretValue(staged.value) : ''
          const contentPlaceholder =
            isExisting && !staged
              ? 'Existing content is hidden. Paste new content to replace.'
              : 'Paste your file content here'
          return (
            <Grid key={fsKey} gutter="md" align="flex-start">
              <Grid.Col span={11}>
                <Stack gap="md">
                  <TextInput
                    label="Name"
                    value={filename}
                    placeholder="e.g. kubeconfig"
                    onChange={(e) => {
                      const next = `filesystem:${e.currentTarget.value}`
                      if (next === fsKey) return
                      // Rename: only allowed for new (unsaved) files.
                      if (isExisting) return
                      cancelSecretChange(fsKey)
                      replaceSecret(next, staged?.value || '')
                    }}
                    disabled={isExisting || !isAdmin}
                  />
                  <Stack gap={4}>
                    <Text size="sm" fw={500}>Content</Text>
                    <Textarea
                      autosize
                      minRows={4}
                      placeholder={contentPlaceholder}
                      value={stagedContent}
                      onChange={(e) => {
                        if (!isAdmin) return
                        replaceSecret(fsKey, encodeSecretValue(e.currentTarget.value))
                      }}
                      disabled={!isAdmin}
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
                  onClick={() => {
                    if (!isAdmin) return
                    if (isExisting) deleteSecret(fsKey)
                    else cancelSecretChange(fsKey)
                  }}
                  aria-label={'Remove configuration file ' + filename}
                  disabled={!isAdmin}
                >
                  <Trash2 size={16} />
                </ActionIcon>
              </Grid.Col>
            </Grid>
          )
        })}
        <Button
          variant="light"
          leftSection={<Plus size={14} />}
          w="fit-content"
          onClick={addEmptyRow}
          disabled={!isAdmin}
        >
          Add
        </Button>
      </Stack>
    </Stack>
  )
}

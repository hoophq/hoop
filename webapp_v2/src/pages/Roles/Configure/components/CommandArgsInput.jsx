import { Stack, Title, Text, TagsInput as MantineTagsInput } from '@mantine/core'
import { useConfigureRoleStore } from '../store'

// Free-form list of command arguments stored as the connection's
// `command` array. Mirrors CLJS server/credentials-step Additional
// Command section. Each value is one argument; pressing Enter (or
// comma) commits the current input as a new tag.
export default function CommandArgsInput() {
  const command = useConfigureRoleStore((s) => s.drafts.command)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)

  return (
    <Stack gap="xs">
      <Title order={5} fw={500}>Additional command</Title>
      <Text size="sm" c="dimmed">
        {"Each argument should be entered separately. Press Enter after each argument to add it to the list. Example: 'python', '-m', 'http.server', '8000'."}
      </Text>
      <MantineTagsInput
        placeholder="Add argument and press Enter"
        value={command}
        onChange={(value) => setDraft({ command: value })}
        clearable
        splitChars={[',']}
      />
    </Stack>
  )
}

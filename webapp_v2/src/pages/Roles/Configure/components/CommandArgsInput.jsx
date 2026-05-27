import { Stack, Title, Text } from '@mantine/core'
import TagsInput from '@/components/TagsInput'
import { useConfigureRoleStore } from '../store'

// Additional command arguments. Stored as the connection's `command`
// array. Mirrors the CLJS server/credentials-step "Additional command"
// section — TagsInput collects each argument as one chip.
export default function CommandArgsInput() {
  const command = useConfigureRoleStore((s) => s.drafts.command)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)

  return (
    <Stack gap="xs">
      <Title order={4}>Additional command</Title>
      <Text size="sm" c="dimmed">
        Each argument should be entered separately. Press Enter after
        each argument to add it to the list.
      </Text>
      <TagsInput
        label="Command Arguments"
        placeholder="Select..."
        value={command}
        onChange={(value) => setDraft({ command: value })}
        clearable
        splitChars={[',']}
      />
      <Text size="xs" c="dimmed">
        {"Example: 'python', '-m', 'http.server', '8000'"}
      </Text>
    </Stack>
  )
}

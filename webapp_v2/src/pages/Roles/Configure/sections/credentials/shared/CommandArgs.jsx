import { Stack, Title, Text } from '@mantine/core'
import TagsInput from '@/components/TagsInput'
import { useCredentialsStore } from '@/pages/Roles/Configure/sections/credentials/store'

// Additional command arguments. Stored as the connection's `command`
// array. Mirrors the CLJS server/credentials-step "Additional command"
// section — TagsInput collects each argument as one chip.
export default function CommandArgs() {
  const command = useCredentialsStore((s) => s.drafts.command)
  const setDraft = useCredentialsStore((s) => s.setDraft)

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

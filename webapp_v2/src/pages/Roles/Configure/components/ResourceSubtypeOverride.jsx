import { Stack, Group, Title, Text, Badge } from '@mantine/core'
import Select from '@/components/Select'
import { useConfigureRoleStore } from '../store'

// Beta section for free-form custom connections that lets the user
// re-classify the resource as a known type (DynamoDB / CloudWatch).
// Mirrors CLJS server/credentials-step resource-subtype-override-section.
//
// The selected value goes straight into the connection's `subtype`
// field on save (matches CLJS process_form.cljs's "effective-subtype"
// behaviour — there's no separate column).
const OPTIONS = [
  { value: 'dynamodb', label: 'DynamoDB' },
  { value: 'cloudwatch', label: 'CloudWatch' },
]

export default function ResourceSubtypeOverride() {
  const subtype = useConfigureRoleStore((s) => s.drafts.subtype)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)

  return (
    <Stack gap="md">
      <Stack gap="xs">
        <Group gap="xs" align="center">
          <Title order={4}>Resource Subtype Override</Title>
          <Badge color="green" variant="filled" size="sm">Beta</Badge>
        </Group>
        <Text size="sm" c="dimmed">
          Configure your resource role for specific resource types.
          Select a subtype only if it matches your actual resource,
          applying the optimal settings for that resource type.
        </Text>
        <Text size="sm" c="dimmed">
          This feature is currently in Beta to streamline resource
          roles to most common resource types.
        </Text>
      </Stack>
      <Select
        placeholder="Select one"
        data={OPTIONS}
        value={subtype || null}
        onChange={(value) => setDraft({ subtype: value || '' })}
        clearable
      />
    </Stack>
  )
}

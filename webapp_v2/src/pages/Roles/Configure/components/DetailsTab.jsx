import { Stack, Title, Text, Badge, Group } from '@mantine/core'
import TextInput from '@/components/TextInput'
import MultiSelect from '@/components/MultiSelect'
import { useConfigureRoleStore } from '../store'
import TagsInput from './TagsInput'

// Details tab: connection name (immutable), attributes (associate from
// the org's attribute catalog), and connection tags (free-form key/value
// labels for filtering and grouping).
export default function DetailsTab({ connection }) {
  const drafts = useConfigureRoleStore((s) => s.drafts)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)
  const attributesList = useConfigureRoleStore((s) => s.attributesList)

  const attributeOptions = attributesList.map((a) => ({
    value: a.name,
    label: a.name,
  }))

  return (
    <Stack gap="xxl" maw={720}>
      <TextInput label="Name" value={connection.name} disabled />

      <Stack gap="sm">
        <Group gap="xs" align="center">
          <Title order={4}>Attributes</Title>
          <Badge size="sm" color="green" variant="filled">NEW</Badge>
        </Group>
        <Text size="sm" c="dimmed">
          Properties that determine how access policies, guardrails, and
          other features apply to this resource role. Attributes are
          evaluated by rules you configure.
        </Text>
        <MultiSelect
          placeholder="Select or type to create"
          searchable
          data={attributeOptions}
          value={drafts.attributes}
          onChange={(value) => setDraft({ attributes: value })}
        />
      </Stack>

      <Stack gap="sm">
        <Title order={4}>Tags</Title>
        <Text size="sm" c="dimmed">
          Labels for filtering, searching, and grouping resource roles in
          your catalog.
        </Text>
        <TagsInput />
      </Stack>
    </Stack>
  )
}

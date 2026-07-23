import { Stack, Title, Text, Group } from '@mantine/core'
import { Award } from 'lucide-react'
import Badge from '@/components/Badge'
import TextInput from '@/components/TextInput'
import MultiSelect from '@/components/MultiSelect'
import { labelForManagedAttribute } from '@/features/ProtectionProfiles/constants'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'
import ConnectionTagsEditor from '@/pages/Roles/Configure/sections/ConnectionTagsEditor'

// Display labels for attribute names strip the `hoop.dev/<category>.`
// namespace prefix the way CLJS tags_utils/extract-label does, so the
// user sees the meaningful tail (e.g. `hoop.dev/infrastructure.cloud`
// renders as `cloud`). Names without the prefix pass through unchanged.
function labelForAttribute(name) {
  const m = name && name.match(/^hoop\.dev\/[^.]+\.([^.]+)$/)
  return m ? m[1] : name
}

// Details tab: connection name (immutable), attributes (associate from
// the org's attribute catalog), and connection tags (free-form key/value
// labels for filtering and grouping).
export default function DetailsTab({ connection }) {
  const drafts = useConfigureRoleStore((s) => s.drafts)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)
  const attributesList = useConfigureRoleStore((s) => s.attributesList)

  const attributeOptions = attributesList.map((a) => ({
    value: a.name,
    label: labelForAttribute(a.name),
  }))

  return (
    <Stack gap="xl" maw={720}>
      <TextInput label="Name" value={connection.name} disabled />

      <Stack gap="md">
        <Stack gap="xs">
          <Group gap="xs" align="center">
            <Title order={4}>Attributes</Title>
            <Badge size="sm" color="green" variant="filled">NEW</Badge>
          </Group>
          <Text size="sm" c="dimmed">
            Properties that determine how access policies, guardrails, and
            other features apply to this resource role. Attributes are
            evaluated by rules you configure.
          </Text>
        </Stack>
        {connection.managed_attributes?.length > 0 && (
          <Group gap="xs">
            {connection.managed_attributes.map((name) => (
              <Badge
                key={name}
                variant="light"
                color="indigo"
                radius="xl"
                tt="none"
                leftSection={<Award size={12} aria-hidden="true" />}
              >
                {labelForManagedAttribute(name)}
              </Badge>
            ))}
            <Text size="xs" c="dimmed">
              Applied by your protection profile — managed by Hoop.
            </Text>
          </Group>
        )}
        <MultiSelect
          placeholder="Select or type to create"
          searchable
          nothingFoundMessage="No attributes created yet. Go to Settings → Attributes to add one."
          data={attributeOptions}
          value={drafts.attributes}
          onChange={(value) => setDraft({ attributes: value })}
        />
      </Stack>

      <Stack gap="md">
        <Stack gap="xs">
          <Title order={4}>Tags</Title>
          <Text size="sm" c="dimmed">
            Labels for filtering, searching, and grouping resource roles in
            your catalog.
          </Text>
        </Stack>
        <ConnectionTagsEditor />
      </Stack>
    </Stack>
  )
}

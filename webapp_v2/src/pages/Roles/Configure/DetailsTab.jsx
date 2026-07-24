import { Stack, Title, Text, Group } from '@mantine/core'
import Badge from '@/components/Badge'
import TextInput from '@/components/TextInput'
import { labelForManagedAttribute } from '@/features/ProtectionProfiles/constants'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'
import AttributesSelect from '@/pages/Roles/Configure/sections/AttributesSelect'
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

  // Hoop-managed attributes (the protection-profile attribute) are regular
  // members of the connection's attribute list — removable and re-addable
  // like any other — but carry the award styling and the profile label.
  const attributeOptions = attributesList.map((a) => ({
    value: a.name,
    label: a.managed_by ? labelForManagedAttribute(a.name) : labelForAttribute(a.name),
    managed: !!a.managed_by,
  }))

  const hasManagedSelected = attributeOptions.some(
    (o) => o.managed && drafts.attributes.includes(o.value),
  )

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
        <AttributesSelect
          placeholder="Select attributes"
          options={attributeOptions}
          value={drafts.attributes}
          onChange={(value) => setDraft({ attributes: value })}
        />
        {hasManagedSelected && (
          <Text size="xs" c="dimmed">
            The award pill is your protection profile attribute. Removing it
            opts this role out of the profile's rules.
          </Text>
        )}
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

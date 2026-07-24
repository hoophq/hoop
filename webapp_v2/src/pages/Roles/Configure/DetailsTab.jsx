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
  const orgProfileAttribute = useConfigureRoleStore((s) => s.orgProfileAttribute)

  // Hoop-managed attributes (protection profiles) flow through the managed
  // pill instead of the regular options.
  const attributeOptions = attributesList
    .filter((a) => !a.managed_by)
    .map((a) => ({
      value: a.name,
      label: labelForAttribute(a.name),
    }))

  // The managed entry offered by the selector: the org's active profile
  // attribute, falling back to whatever managed attribute the connection
  // already carries (covers a stale profile or a failed org-profile fetch).
  const managedSource = orgProfileAttribute ?? connection.managed_attributes?.[0] ?? null
  const managedOptions = managedSource
    ? [{ value: managedSource, label: labelForManagedAttribute(managedSource) }]
    : []
  const managedValue =
    drafts.protection_profile_enabled && managedSource ? [managedSource] : []

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
          managedOptions={managedOptions}
          managedValue={managedValue}
          onManagedChange={(names) =>
            setDraft({ protection_profile_enabled: names.length > 0 })
          }
        />
        {managedOptions.length > 0 && (
          <Text size="xs" c="dimmed">
            The award pill is your protection profile attribute, managed by
            Hoop. Removing it opts this role out of the profile rules.
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

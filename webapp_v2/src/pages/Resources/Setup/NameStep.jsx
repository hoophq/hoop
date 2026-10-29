import { Group, Image, Paper, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import SectionRow from '@/components/SectionRow'
import TextInput from '@/components/TextInput'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import StepHeader from './components/StepHeader'
import { useResourceSetupStore } from './store'

const BADGES = { new: { label: 'NEW', color: 'green' }, beta: { label: 'BETA', color: 'indigo' } }

function badgeOf(badge) {
  return BADGES[badge] ?? { label: badge.charAt(0).toUpperCase() + badge.slice(1), color: 'gray' }
}

export default function NameStep() {
  const setup = useResourceSetupStore((s) => s.setup)
  const name = useResourceSetupStore((s) => s.name)
  const setName = useResourceSetupStore((s) => s.setName)
  const entry = useConnectionsMetadataStore((s) => s.getConnection(setup.subtype))
  const iconFor = useConnectionIconGetter()

  return (
    <Stack gap="xxlAlt" pt="xl">
      <StepHeader
        title="Setup your Resource"
        description="Complete the following information to setup your Resource."
      />

      <SectionRow title="Resource type" description="The type you picked in the resource catalog.">
        <Paper withBorder p="md" radius="md">
          <Stack gap="lg">
            <Group justify="space-between">
              <Image src={iconFor(setup)} alt={setup.subtype} w={24} h={24} />
              <Group gap={4}>
                {(entry?.badges ?? []).map(badgeOf).map(({ label, color }) => (
                  <Badge key={label} size="xs" variant="filled" color={color}>
                    {label}
                  </Badge>
                ))}
              </Group>
            </Group>
            <Text fw={700}>{entry?.name ?? setup.subtype}</Text>
          </Stack>
        </Paper>
      </SectionRow>

      <SectionRow title="Name" description="Used to identify this Resource in your environment.">
        <TextInput
          label="Resource name"
          placeholder={`e.g. my-${setup.subtype}`}
          value={name}
          required
          onChange={(e) => setName(e.currentTarget.value)}
        />
      </SectionRow>
    </Stack>
  )
}

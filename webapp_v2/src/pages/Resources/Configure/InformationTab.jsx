import { Image, Paper, Stack, Text } from '@mantine/core'
import SectionRow from '@/components/SectionRow'
import TextInput from '@/components/TextInput'
import { useConnectionIconGetter } from '@/utils/connectionIcons'

const capitalize = (s = '') => s.charAt(0).toUpperCase() + s.slice(1)

export default function InformationTab({ resource, name, saving, onNameChange }) {
  const iconFor = useConnectionIconGetter()
  return (
    <Stack gap="xxlAlt">
      <SectionRow title="Resource type" description="The kind of system this resource connects to.">
        <Paper withBorder radius="lg" p="md" h={110}>
          <Stack h="100%" justify="space-between">
            <Image src={iconFor(resource)} alt="" w={24} h={24} fit="contain" />
            <Text fw={700}>{capitalize(resource.type)}</Text>
          </Stack>
        </Paper>
      </SectionRow>

      <SectionRow title="Resource ID" description="Used to identify this Resource in your environment.">
        <TextInput
          label="Name"
          placeholder="Enter resource name"
          value={name}
          disabled={saving}
          onChange={(event) => onNameChange(event.currentTarget.value.replace(/\s+/g, '-'))}
        />
      </SectionRow>
    </Stack>
  )
}

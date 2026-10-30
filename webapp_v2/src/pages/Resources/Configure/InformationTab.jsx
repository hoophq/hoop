import { Grid, Image, Paper, Stack, Text, Title } from '@mantine/core'
import TextInput from '@/components/TextInput'
import { useConnectionIconGetter } from '@/utils/connectionIcons'

const capitalize = (s = '') => s.charAt(0).toUpperCase() + s.slice(1)

// The CLJS page's 3/4 split, wider on the left than SectionRow's 2/5.
function Row({ title, description, children }) {
  return (
    <Grid columns={7} gutter="xlAlt">
      <Grid.Col span={{ base: 7, sm: 3 }}>
        <Stack gap="xsAlt">
          <Title order={4} fw={500}>
            {title}
          </Title>
          <Text size="sm" c="dimmed">
            {description}
          </Text>
        </Stack>
      </Grid.Col>
      <Grid.Col span={{ base: 7, sm: 4 }}>{children}</Grid.Col>
    </Grid>
  )
}

export default function InformationTab({ resource, name, saving, onNameChange }) {
  const iconFor = useConnectionIconGetter()
  return (
    <Stack gap="xxxlAlt">
      <Row title="Resource type" description="The kind of system this resource connects to.">
        <Paper withBorder radius="lg" p="md" h={110} bg="var(--hoop-surface-raised)">
          <Stack h="100%" justify="space-between">
            <Image src={iconFor(resource)} alt="" w={24} h={24} fit="contain" />
            <Text fw={700}>{capitalize(resource.type)}</Text>
          </Stack>
        </Paper>
      </Row>

      <Row title="Resource ID" description="Used to identify this Resource in your environment.">
        <TextInput
          label="Name"
          placeholder="Enter resource name"
          value={name}
          disabled={saving}
          onChange={(event) => onNameChange(event.currentTarget.value.replace(/\s+/g, '-'))}
        />
      </Row>
    </Stack>
  )
}

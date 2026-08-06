import { Flex, Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'

export default function TemplateListItem({ template, onConfigure, onDelete }) {
  return (
    <Flex p="lg" align="center" justify="space-between" gap="md">
      <Stack gap="xs" flex={1} miw={0}>
        <Text fw={500} fz="lg">
          {template.name}
        </Text>
        {template.description && (
          <Text size="sm" c="dimmed">
            {template.description}
          </Text>
        )}
      </Stack>
      <Group gap="sm" wrap="nowrap">
        <Button variant="subtle" color="red" onClick={() => onDelete(template)}>
          Delete
        </Button>
        <Button variant="default" onClick={() => onConfigure(template.id)}>
          Configure
        </Button>
      </Group>
    </Flex>
  )
}

import { Stack, Text, Button } from '@mantine/core'

function EmptyState({ icon: Icon, title, description, action }) {
  return (
    <Stack align="center" gap="md" py="xl">
      {Icon && <Icon size={40} color="var(--mantine-color-dimmed)" />}
      <Stack align="center" gap={4}>
        <Text fw={500}>{title}</Text>
        {description && (
          <Text size="sm" c="dimmed" ta="center" maw={380}>
            {description}
          </Text>
        )}
      </Stack>
      {action && (
        <Button variant="light" onClick={action.onClick}>
          {action.label}
        </Button>
      )}
    </Stack>
  )
}

export default EmptyState

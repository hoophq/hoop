import { Box, Group, Image, Stack, Text } from '@mantine/core'

const capitalize = (s = '') => s.charAt(0).toUpperCase() + s.slice(1)

export default function RoleSummary({ role, iconSrc, iconAlt }) {
  const online = role.status === 'online'
  return (
    <Group gap="md" wrap="nowrap" miw={0}>
      <Image src={iconSrc} alt={iconAlt} w={24} h={24} fit="contain" loading="lazy" />
      <Stack gap={0} miw={0}>
        <Text component="h3" fw={500} truncate>
          {role.name}
        </Text>
        <Text size="xs" c="dimmed">
          {role.resource_name}
        </Text>
        <Group gap={4}>
          <Box w={6} h={6} bg={online ? 'green.6' : 'red.6'} bdrs="xl" aria-hidden="true" />
          <Text size="xs" c="dimmed">
            {capitalize(role.status)}
          </Text>
        </Group>
      </Stack>
    </Group>
  )
}

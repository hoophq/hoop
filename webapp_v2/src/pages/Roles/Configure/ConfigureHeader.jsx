import { Group, Stack, Title, Text, Image } from '@mantine/core'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { canTestConnection, isSidecarMirror } from '@/utils/connectionPolicy'

// Page header. Shows "Configure" + the connection's icon and name on the
// left, and the Test Connection action on the right.

export default function ConfigureHeader({ connection, testing, onTest }) {
  const getConnectionIcon = useConnectionIconGetter()
  const iconUrl = getConnectionIcon(connection)
  const mirror = isSidecarMirror(connection)
  const canTest = !mirror && canTestConnection(connection)
  return (
    <Group justify="space-between" align="flex-start">
      <Stack gap="xs">
        <Title order={2}>Configure</Title>
        <Group gap="sm" align="center">
          <Image src={iconUrl} w={20} h={20} alt="" />
          <Text size="md">{connection.name}</Text>
          {mirror && (
            <Badge variant="light" color="gray" tag>
              via sidecar
            </Badge>
          )}
        </Group>
      </Stack>

      {canTest && (
        <Button variant="default" loading={testing} onClick={onTest}>
          Test Connection
        </Button>
      )}
    </Group>
  )
}

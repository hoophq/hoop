import { Group, Stack, Title, Text, Button, Image } from '@mantine/core'
import { getConnectionIcon } from '@/utils/connectionIcons'

// Page header. Shows "Configure" + the connection's icon and name on the
// left, and the Test Connection action on the right.
//
// Test Connection is only meaningful for connection types that can run a
// ping query (databases, AWS shell connections). For everything else the
// button is hidden — matches the CLJS can-test-connection? predicate.
const TESTABLE_TYPES = new Set([
  'database',
  'application',
])
const TESTABLE_SUBTYPES = new Set([
  'postgres',
  'mysql',
  'mssql',
  'oracledb',
  'mongodb',
  'dynamodb',
  'cloudwatch',
])

function canTestConnection({ type, subtype }) {
  if (TESTABLE_SUBTYPES.has(subtype)) return true
  return TESTABLE_TYPES.has(type)
}

export default function ConfigureHeader({ connection, testing, onTest }) {
  const iconUrl = getConnectionIcon(connection)
  const canTest = canTestConnection(connection)
  return (
    <Group justify="space-between" align="flex-start">
      <Stack gap="xs">
        <Title order={2}>Configure</Title>
        <Group gap="sm" align="center">
          <Image src={iconUrl} w={20} h={20} alt="" />
          <Text size="md">{connection.name}</Text>
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

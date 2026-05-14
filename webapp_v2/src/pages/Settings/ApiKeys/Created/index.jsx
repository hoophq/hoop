import { useLocation, useNavigate } from 'react-router-dom'
import { Alert, Box, Button, Flex, Group, Stack, Text, TextInput, Title } from '@mantine/core'
import { CheckCircle, Info } from 'lucide-react'
import CopyButton from '@/components/CopyButton'

export default function ApiKeysCreated() {
  const navigate = useNavigate()
  const { state } = useLocation()
  const rawKey = state?.key?.key ?? ''

  return (
    <Flex justify="center" align="center" mih="60vh">
      <Box maw={520} w="100%">
        <Stack gap="xl">
          <Flex direction="column" align="center" gap="sm">
            <Box p="md" bg="green.1" style={{ borderRadius: '50%' }}>
              <CheckCircle size={40} color="var(--mantine-color-green-7)" />
            </Box>
            <Title order={2} ta="center">
              Your API key is ready!
            </Title>
          </Flex>

          <Alert icon={<Info size={16} />} color="yellow" variant="light">
            This is the only time you'll see this key. Copy and store it securely.
          </Alert>

          <Stack gap="xs">
            <Text size="sm" fw={600} c="dimmed">
              Copy your API Key
            </Text>
            <Group gap="sm" wrap="nowrap">
              <TextInput
                value={rawKey}
                readOnly
                flex={1}
                ff="monospace"
                size="sm"
              />
              <CopyButton value={rawKey} label="Copy API Key" size="md" />
            </Group>
          </Stack>

          <Flex justify="center">
            <Button
              variant="subtle"
              color="gray"
              onClick={() => navigate('/settings/api-keys')}
            >
              ← Back to API keys
            </Button>
          </Flex>
        </Stack>
      </Box>
    </Flex>
  )
}

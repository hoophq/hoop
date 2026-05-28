import { useLocation, useNavigate } from 'react-router-dom'
import { Alert, Anchor, Box, Button, Flex, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { Bot, Info, KeyRound } from 'lucide-react'
import CopyButton from '@/components/CopyButton'
import CodeSnippet from '@/components/CodeSnippet'
import TextInput from '@/components/TextInput'
import { useUserStore } from '@/stores/useUserStore'

const LIST_PATH = '/features/ai-agents-identities'
const MCP_DOCS_URL = 'https://hoop.dev/docs/learn/features/mcp-server#mcp-server'
const CLI_DOCS_URL = 'https://hoop.dev/docs/clients/cli#command-line'

function buildMcpSnippet(apiUrl, key) {
  const config = {
    mcpServers: {
      hoop: {
        url: `${apiUrl}/api/mcp`,
        headers: {
          Authorization: `Bearer ${key}`,
        },
      },
    },
  }
  return JSON.stringify(config, null, 2)
}

function buildCliSnippet(apiUrl, key) {
  return [
    "# After installing hoop's CLI",
    '',
    'hoop config create \\',
    `    --api-key ${key} \\`,
    `    --api-url ${apiUrl}`,
  ].join('\n')
}

export default function AiAgentsIdentitiesCreated() {
  const navigate = useNavigate()
  const { state } = useLocation()
  const rawKey = state?.agent?.key ?? ''
  const storedApiUrl = useUserStore((s) => s.apiUrl)
  const apiUrl = storedApiUrl || window.location.origin

  return (
    <Flex justify="center" mih="60vh">
      <Box maw={552} w="100%">
        <Stack gap="xl">
          <Flex direction="column" align="center" gap="sm">
            <ThemeIcon size={72} radius="xl" color="indigo" variant="light">
              <Bot size={32} />
            </ThemeIcon>
            <Title order={2} ta="center">
              Your AI Agent Identity is ready!
            </Title>
          </Flex>

          <Alert icon={<Info size={16} />} color="yellow" variant="light">
            {"This is the only time you'll see this key. Copy and store it securely."}
          </Alert>

          <Stack gap="xs">
            <Text size="sm" fw={600}>
              Agent identity authorization key
            </Text>
            <TextInput
              value={rawKey}
              readOnly
              ff="monospace"
              size="sm"
              leftSection={<KeyRound size={14} color="var(--mantine-color-gray-7)" />}
              rightSection={<CopyButton value={rawKey} label="Copy AI Agent key" />}
            />
          </Stack>

          <Stack gap="xs">
            <Title order={4}>Agentic usage via MCP</Title>
            <Text size="sm" c="dimmed">
              {"Add Hoop's MCP to your .mcp.json file. "}
              <Anchor href={MCP_DOCS_URL} target="_blank" rel="noopener noreferrer" size="sm">
                {"Learn more about Hoop's MCP."}
              </Anchor>
            </Text>
            <CodeSnippet code={buildMcpSnippet(apiUrl, rawKey)} />
          </Stack>

          <Stack gap="xs">
            <Title order={4}>{"Agentic usage via Hoop's CLI"}</Title>
            <Text size="sm" c="dimmed">
              {"Install Hoop's CLI in the agent's machine. "}
              <Anchor href={CLI_DOCS_URL} target="_blank" rel="noopener noreferrer" size="sm">
                Learn how to install our CLI.
              </Anchor>
            </Text>
            <CodeSnippet code={buildCliSnippet(apiUrl, rawKey)} />
          </Stack>

          <Flex justify="center">
            <Button variant="subtle" color="gray" onClick={() => navigate(LIST_PATH)}>
              ← Back to AI Agents Identities
            </Button>
          </Flex>
        </Stack>
      </Box>
    </Flex>
  )
}

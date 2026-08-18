import { Box, Stack, Text, Title } from '@mantine/core'
import Code from '@/components/Code'
import CodeSnippet from '@/components/CodeSnippet'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import { InstructionBlock } from '../components/InstructionBlock'
import { CLAUDE_CODE_DOCS_URL } from '../constants'

/**
 * Claude Code is configured through a settings.json rather than a connection
 * string, so this renderer is a short walkthrough instead of a field list.
 *
 * Note it uses the hostname the gateway returned, not the browser's: the
 * http-proxy responses already rewrite 0.0.0.0/127.0.0.1 to localhost.
 */
export function ClaudeCodeCredentials({ credentials, connectionName }) {
  const { hostname, port, proxy_token, vertex_project_id, vertex_region } = credentials
  const baseUrl = `${window.location.protocol}//${hostname}:${port}`
  const isVertex = Boolean(vertex_project_id)

  // Vertex mode forwards the full Vertex path through hoop, so the base URL has
  // to keep the /v1 segment the Anthropic Vertex SDK appends /projects/... to.
  const config = isVertex
    ? {
        env: {
          CLAUDE_CODE_USE_VERTEX: '1',
          CLAUDE_CODE_SKIP_VERTEX_AUTH: '1',
          ANTHROPIC_VERTEX_PROJECT_ID: vertex_project_id,
          CLOUD_ML_REGION: vertex_region,
          ANTHROPIC_VERTEX_BASE_URL: `${baseUrl}/v1`,
          ANTHROPIC_AUTH_TOKEN: proxy_token,
        },
      }
    : {
        env: {
          ANTHROPIC_BASE_URL: baseUrl,
          ANTHROPIC_CUSTOM_HEADERS: `Authorization: ${proxy_token}`,
        },
      }

  const firstVar = isVertex ? 'ANTHROPIC_VERTEX_BASE_URL' : 'ANTHROPIC_BASE_URL'
  const secondVar = isVertex ? 'ANTHROPIC_AUTH_TOKEN' : 'ANTHROPIC_CUSTOM_HEADERS'

  return (
    <Stack gap="lg">
      <InstructionBlock
        heading="Create or modify settings.json"
        text="Locate this file or create and access it via your preferred IDE"
        code="~/.claude/settings.json"
      />

      <InstructionBlock
        heading={"If the file or folder doesn't exist"}
        text="Make sure the folder exists and create it:"
        code="mkdir -p ~/.claude && touch ~/.claude/settings.json"
      />

      <Stack gap="xs">
        <Box>
          <Title order={3} fz="md" fw={700}>
            Add the following configuration
          </Title>
          <Text fz="sm" c="dimmed">
            {'Modify the following values accordingly. If you have more settings, you can leave them, you only need to modify '}
            <Code>{firstVar}</Code>
            {' and '}
            <Code>{secondVar}</Code>
            {'.'}
          </Text>
        </Box>
        <CodeSnippet code={JSON.stringify(config, null, 2)} />
        <Box pt="xs">
          <Text fz="sm" c="dimmed" mb="xs">
            Or run this command to apply automatically:
          </Text>
          <CodeSnippet code={`hoop claude configure ${connectionName}`} />
        </Box>
      </Stack>

      <Stack gap="xs">
        <Box>
          <Title order={3} fz="md" fw={700}>
            In your favorite IDE
          </Title>
          <Text fz="sm" c="dimmed">
            Open your IDE and run the Claude Code plugin.
          </Text>
        </Box>
        <DocsBtnCallOut
          href={CLAUDE_CODE_DOCS_URL}
          text="See supported IDEs at Claude Code documentation."
        />
      </Stack>

      <InstructionBlock
        heading="In the Terminal"
        text="Run Claude Code Command Line Interface"
        code="$ claude"
      />
    </Stack>
  )
}

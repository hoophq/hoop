import { useEffect, useState } from 'react'
import { Box, Collapse, Group, Text, UnstyledButton } from '@mantine/core'
import { ChevronDown, ChevronUp, Sparkles } from 'lucide-react'
import Badge from '@/components/Badge'
import CodeSnippet from '@/components/CodeSnippet'
import { useAiSessionAnalyzerStore } from '../../store'

// Read-only view of the prompt Hoop prepends, so admins can see it while
// writing their own.
export default function SystemPromptPreview() {
  const [expanded, setExpanded] = useState(false)

  const systemPrompt = useAiSessionAnalyzerStore((s) => s.systemPrompt)
  const systemPromptStatus = useAiSessionAnalyzerStore((s) => s.systemPromptStatus)
  const fetchSystemPrompt = useAiSessionAnalyzerStore((s) => s.fetchSystemPrompt)

  useEffect(() => {
    fetchSystemPrompt()
  }, [fetchSystemPrompt])

  return (
    <Box bd="1px solid var(--mantine-color-default-border)" bdrs="md">
      <UnstyledButton
        w="100%"
        px="md"
        py="sm"
        onClick={() => setExpanded((value) => !value)}
        aria-expanded={expanded}
      >
        <Group justify="space-between" align="center" wrap="nowrap">
          <Group gap="xs" align="center" wrap="nowrap">
            <Sparkles size={14} color="var(--mantine-color-indigo-6)" />
            <Text size="sm" fw={500}>
              {"Hoop's appended system prompt"}
            </Text>
            <Badge variant="light" color="gray" radius="xl">
              Read-only
            </Badge>
          </Group>
          {expanded ? (
            <ChevronUp size={14} color="var(--mantine-color-dimmed)" />
          ) : (
            <ChevronDown size={14} color="var(--mantine-color-dimmed)" />
          )}
        </Group>
      </UnstyledButton>

      <Collapse in={expanded}>
        <Box px="md" pb="md">
          {systemPromptStatus === 'error' ? (
            <Text size="sm" c="red">
              Failed to load the system prompt. Refresh and try again.
            </Text>
          ) : systemPrompt ? (
            <CodeSnippet code={systemPrompt} />
          ) : (
            <Text size="sm" c="dimmed">
              Loading prompt...
            </Text>
          )}
        </Box>
      </Collapse>
    </Box>
  )
}

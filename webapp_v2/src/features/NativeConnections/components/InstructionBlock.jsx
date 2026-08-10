import { Box, Stack, Text, Title } from '@mantine/core'
import CodeSnippet from '@/components/CodeSnippet'

/** Heading + prose + a code block. Used by the Claude Code walkthrough. */
export function InstructionBlock({ heading, text, code, children }) {
  return (
    <Stack gap="xs">
      <Box>
        <Title order={3} fz="md" fw={700}>
          {heading}
        </Title>
        {text && (
          <Text fz="sm" c="dimmed">
            {text}
          </Text>
        )}
      </Box>
      {code !== undefined && <CodeSnippet code={code} />}
      {children}
    </Stack>
  )
}

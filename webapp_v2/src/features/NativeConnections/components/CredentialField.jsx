import { Stack, Text } from '@mantine/core'
import CodeSnippet from '@/components/CodeSnippet'

/** Labelled, copyable credential value — the unit every renderer is built from. */
export function CredentialField({ label, value }) {
  if (value === undefined || value === null || value === '') return null
  return (
    <Stack gap="xs">
      <Text fz="sm" fw={700}>
        {label}
      </Text>
      <CodeSnippet code={String(value)} />
    </Stack>
  )
}

import { Group, Stack, Text, Paper, ThemeIcon } from '@mantine/core'
import { Check } from 'lucide-react'
import Button from '@/components/Button'
import MarkdownText from '@/components/MarkdownText'

// The "set" state of SecretField: an existing inline secret. We never
// show the value — just a confirmation that it's stored, with a
// Replace action. References (provider-prefixed values) render the
// reference text verbatim, since it's safe and useful to display.
export default function ReadOnlyStatus({
  label,
  description,
  required,
  isReference,
  referenceText,
  onReplace,
}) {
  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center">
        <Group gap={4}>
          <Text size="sm" fw={500}>{label}</Text>
          {required && <Text size="sm" c="red">*</Text>}
        </Group>
        <Group gap="xs">
          <Button size="xs" variant="default" onClick={onReplace}>
            Replace
          </Button>
        </Group>
      </Group>
      {description && <MarkdownText>{description}</MarkdownText>}
      <Paper p="sm" radius="sm" bg="gray.0" withBorder>
        {isReference ? (
          <Group gap="xs" wrap="nowrap">
            <ThemeIcon size="sm" color="indigo" variant="light">
              <Check size={12} />
            </ThemeIcon>
            <Text size="sm" ff="monospace" lh={1.2}>
              {referenceText}
            </Text>
          </Group>
        ) : (
          <Group gap="xs">
            <ThemeIcon size="sm" color="green" variant="light">
              <Check size={12} />
            </ThemeIcon>
            <Text size="sm" c="dimmed">Set</Text>
          </Group>
        )}
      </Paper>
    </Stack>
  )
}

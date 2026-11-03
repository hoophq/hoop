import { Group, Paper, Stack, Text } from '@mantine/core'
import { Download, FileDown, Info } from 'lucide-react'
import Button from '@/components/Button'
import { humanizeSize } from '../viewers/exec/output'

// Figma "Large Input / Large Output - Download": the content is not shown here,
// the user downloads it. `size` is in bytes; `onDownload` is absent when
// downloads are disabled.
export default function PreviewUnavailable({ title = 'Session Output', size, message, onDownload }) {
  return (
    <Paper variant="muted" radius="md" p="lgAlt">
      <Stack gap="md">
        <Group gap="xs">
          <FileDown size={20} aria-hidden />
          <Text size="sm" fw={700}>
            {title}
          </Text>
        </Group>
        {size > 0 && (
          <Stack gap={4}>
            <Text size="sm" c="dimmed">
              File size
            </Text>
            <Text size="sm">{humanizeSize(size)}</Text>
          </Stack>
        )}
        <Group gap="sm" wrap="nowrap" align="stretch">
          <Paper variant="strong" radius="sm" px="smAlt" py="xsAlt" flex={1}>
            <Group gap="xs" wrap="nowrap">
              <Info size={16} aria-hidden />
              <Text size="sm">{message}</Text>
            </Group>
          </Paper>
          {onDownload && (
            <Button variant="light" rightSection={<Download size={16} />} onClick={onDownload}>
              Download file
            </Button>
          )}
        </Group>
      </Stack>
    </Paper>
  )
  }

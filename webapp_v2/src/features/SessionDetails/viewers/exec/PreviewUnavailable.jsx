import { Box, Group, Stack, Text } from '@mantine/core'
import { Download, FileDown, Info } from 'lucide-react'
import Button from '@/components/Button'
import { humanizeSize } from './output'
import classes from './exec.module.css'

// Figma "Output | Large Output - Download": the output is not shown here, the
// user downloads it. `size` is in bytes; `onDownload` is absent when downloads
// are disabled.
export default function PreviewUnavailable({ size, message, onDownload }) {
  return (
    <Stack gap="md" className={classes.preview}>
      <Group gap="xs">
        <FileDown size={20} aria-hidden />
        <Text size="sm" fw={700}>
          Session Output
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
        <Group gap="xs" wrap="nowrap" flex={1} className={classes.notice}>
          <Info size={16} aria-hidden />
          <Text size="sm">{message}</Text>
        </Group>
        {onDownload && (
          <Button variant="light" rightSection={<Download size={16} />} onClick={onDownload}>
            Download file
          </Button>
        )}
      </Group>
    </Stack>
  )
}

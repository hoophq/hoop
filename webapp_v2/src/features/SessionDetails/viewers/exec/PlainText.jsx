import { useMemo } from 'react'
import { Box } from '@mantine/core'
import { useClipboard } from '@mantine/hooks'
import { Check, Copy } from 'lucide-react'
import Button from '@/components/Button'
import { useUserStore } from '@/stores/useUserStore'
import { LINE_HEIGHT, splitLines } from './output'
import VirtualLines from './VirtualLines'
import classes from './exec.module.css'

// The box grows with the output up to the 384px it had in the previous page.
const MAX_HEIGHT = 384
const PADDING = 24

// Figma "Output | Plain Text": the output in a dark box, virtualized, with Copy.
export default function PlainText({ output }) {
  const disableClipboard = useUserStore((s) => s.disableClipboard)
  const clipboard = useClipboard({ timeout: 2000 })
  const lines = useMemo(() => splitLines(output), [output])
  const height = Math.min(lines.length * LINE_HEIGHT + PADDING, MAX_HEIGHT)

  return (
    <Box
      className={classes.terminal}
      h={height}
      data-no-select={disableClipboard || undefined}
      onCopy={disableClipboard ? (e) => e.preventDefault() : undefined}
    >
      {!disableClipboard && (
        <Button
          size="xs"
          variant="filled"
          color="dark"
          className={classes.copy}
          leftSection={clipboard.copied ? <Check size={14} /> : <Copy size={14} />}
          onClick={() => clipboard.copy(output)}
        >
          {clipboard.copied ? 'Copied' : 'Copy'}
        </Button>
      )}
      <VirtualLines lines={lines} />
    </Box>
  )
}

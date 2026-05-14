import { CopyButton as MantineCopyButton, ActionIcon, Tooltip } from '@mantine/core'
import { Copy, Check } from 'lucide-react'

/**
 * Icon button that copies `value` to the clipboard.
 * Shows a checkmark for 2 seconds after copying.
 *
 * Usage:
 *   <CopyButton value="secret-key-here" />
 *   <CopyButton value={key} label="Copy API Key" />
 */
export default function CopyButton({ value, label, size = 'sm', ...props }) {
  return (
    <MantineCopyButton value={value} timeout={2000}>
      {({ copied, copy }) => (
        <Tooltip label={copied ? 'Copied!' : (label ?? 'Copy')} withArrow>
          <ActionIcon
            color={copied ? 'green' : 'gray'}
            variant="subtle"
            onClick={copy}
            size={size}
            aria-label={label ?? 'Copy'}
            {...props}
          >
            {copied ? <Check size={14} /> : <Copy size={14} />}
          </ActionIcon>
        </Tooltip>
      )}
    </MantineCopyButton>
  )
}

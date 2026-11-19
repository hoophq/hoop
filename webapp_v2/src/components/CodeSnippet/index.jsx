import { Box, CopyButton } from '@mantine/core'
import { Copy, Check } from 'lucide-react'
import Button from '@/components/Button'
import { useUserStore } from '@/stores/useUserStore'
import classes from './CodeSnippet.module.css'

function CodeSnippet({ code, variant = 'black' }) {
  const disableClipboard = useUserStore(s => s.disableClipboard)

  return (
    <Box pos="relative" className={classes.root} data-variant={variant} p="sm">
      <div className={classes.scroll}>
        <pre className={classes.pre}>{code}</pre>
      </div>

      {!disableClipboard && (
        <Box pos="absolute" top={6} right={6}>
          <CopyButton value={code} timeout={2000}>
            {({ copied, copy }) => (
              <Button
                variant="subtle"
                color={copied ? 'teal' : 'gray'}
                size="compact-xs"
                onClick={copy}
                leftSection={copied ? <Check size={12} /> : <Copy size={12} />}
                className={classes.copyBtn}
                data-copied={copied || undefined}
              >
                {copied ? 'Copied' : 'Copy'}
              </Button>
            )}
          </CopyButton>
        </Box>
      )}
    </Box>
  )
}

export default CodeSnippet

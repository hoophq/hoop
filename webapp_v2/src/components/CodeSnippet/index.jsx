import { useState } from 'react'
import { Box, ActionIcon, CopyButton } from '@mantine/core'
import { Copy, Check } from 'lucide-react'
import { useUserStore } from '@/stores/useUserStore'
import classes from './CodeSnippet.module.css'

function CodeSnippet({ code }) {
  const [hovered, setHovered] = useState(false)
  const disableClipboard = useUserStore(s => s.disableClipboard)

  return (
    <Box
      pos="relative"
      className={classes.root}
      p="sm"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <div className={classes.scroll}>
        <pre className={classes.pre}>{code}</pre>
      </div>

      {!disableClipboard && (
        <Box
          pos="absolute"
          top={6}
          right={6}
          className={classes.copyBtn}
          data-visible={hovered || undefined}
        >
          <CopyButton value={code} timeout={2000}>
            {({ copied, copy }) => (
              <ActionIcon
                variant="subtle"
                color={copied ? 'teal' : 'gray'}
                size="sm"
                onClick={copy}
              >
                {copied ? <Check size={13} /> : <Copy size={13} />}
              </ActionIcon>
            )}
          </CopyButton>
        </Box>
      )}
    </Box>
  )
}

export default CodeSnippet

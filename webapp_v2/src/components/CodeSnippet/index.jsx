import { useState } from 'react'
import { Box, ActionIcon, CopyButton } from '@mantine/core'
import { Copy, Check } from 'lucide-react'

function CodeSnippet({ code }) {
  const [hovered, setHovered] = useState(false)

  return (
    <Box
      pos="relative"
      style={{ backgroundColor: 'var(--mantine-color-dark-8)', borderRadius: 8 }}
      p="sm"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <Box style={{ overflowX: 'auto', paddingRight: 28 }}>
        <pre
          style={{
            fontFamily: 'monospace',
            fontSize: 13,
            color: 'var(--mantine-color-gray-2)',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
            margin: 0,
          }}
        >
          {code}
        </pre>
      </Box>

      <Box
        pos="absolute"
        top={6}
        right={6}
        style={{ opacity: hovered ? 1 : 0, transition: 'opacity 120ms ease' }}
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
    </Box>
  )
}

export default CodeSnippet

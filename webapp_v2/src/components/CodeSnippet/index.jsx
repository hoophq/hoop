import { useState } from 'react'
import { Group, Code, ActionIcon, Tooltip, CopyButton } from '@mantine/core'
import { Copy, Check } from 'lucide-react'

function CodeSnippet({ code, language }) {
  return (
    <Group gap="xs" align="flex-start" wrap="nowrap">
      <Code block style={{ flex: 1, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
        {code}
      </Code>
      <CopyButton value={code} timeout={2000}>
        {({ copied, copy }) => (
          <Tooltip label={copied ? 'Copied!' : 'Copy'} withArrow>
            <ActionIcon
              variant="subtle"
              color={copied ? 'teal' : 'gray'}
              onClick={copy}
              mt={4}
            >
              {copied ? <Check size={16} /> : <Copy size={16} />}
            </ActionIcon>
          </Tooltip>
        )}
      </CopyButton>
    </Group>
  )
}

export default CodeSnippet

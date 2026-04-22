import { Box, Flex, Text, Anchor } from '@mantine/core'
import { ArrowUpRight } from 'lucide-react'

function DocsBtnCallOut({ text, href }) {
  return (
    <Box
      mt="md"
      style={{
        border: '1px solid var(--mantine-color-default-border)',
        borderRadius: 12,
        width: 'fit-content',
        padding: '10px 14px',
      }}
    >
      <Anchor href={href} target="_blank" rel="noopener noreferrer" underline="never">
        <Flex gap="xs" align="center">
          <ArrowUpRight size={16} style={{ color: 'var(--mantine-color-dimmed)', flexShrink: 0 }} />
          <Text size="sm" c="dimmed">{text}</Text>
        </Flex>
      </Anchor>
    </Box>
  )
}

export default DocsBtnCallOut

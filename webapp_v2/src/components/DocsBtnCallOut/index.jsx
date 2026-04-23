import { Box, Flex, Text, Anchor } from '@mantine/core'
import { ArrowUpRight } from 'lucide-react'

function DocsBtnCallOut({ text, href }) {
  return (
    <Box
      mt="md"
      bd="1px solid var(--mantine-color-default-border)"
      p="sm"
      radius="lg"
      w="fit-content"
    >
      <Anchor href={href} target="_blank" rel="noopener noreferrer" underline="never">
        <Flex gap="xs" align="center">
          <ArrowUpRight size={16} color="var(--mantine-color-dimmed)" />
          <Text size="sm" c="dimmed">{text}</Text>
        </Flex>
      </Anchor>
    </Box>
  )
}

export default DocsBtnCallOut

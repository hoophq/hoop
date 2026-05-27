import { Text } from '@mantine/core'
import Code from '@/components/Code'

export default function EventDescription({ text, ...textProps }) {
  const parts = (text || '').split('`')
  return (
    <Text {...textProps}>
      {parts.map((p, i) =>
        i % 2 === 0 ? p : <Code key={i} display="inline" w="auto">{p}</Code>,
      )}
    </Text>
  )
}

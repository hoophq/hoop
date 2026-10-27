import { Text } from '@mantine/core'

// A field label with a grey "(Optional)" after it, as the analyzer design shows.
export default function OptionalLabel({ children }) {
  return (
    <>
      {children + ' '}
      <Text span inherit fw={400} c="dimmed">
        (Optional)
      </Text>
    </>
  )
}

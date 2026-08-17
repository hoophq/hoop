import { Text, VisuallyHidden } from '@mantine/core'
import useCountdown from '@/hooks/useCountdown'

/**
 * Live countdown text.
 *
 * The digits are aria-hidden and paired with a static label: they change every
 * second, and inside an accordion control that would make a screen reader
 * re-announce the whole row once per second.
 */
export function SessionTimer({ expireAt, fw = 700, fz = 'sm' }) {
  const { label } = useCountdown(expireAt)
  if (!label) return null
  return (
    <>
      <Text component="span" fz={fz} fw={fw} aria-hidden="true">
        {label}
      </Text>
      <VisuallyHidden>Active session</VisuallyHidden>
    </>
  )
}

import { Code as MantineCode } from '@mantine/core'

/**
 * Code wrapper.
 * - Subtle indigo tint so identifiers stand out from body text.
 * - `display: inline-block` + `w="fit-content"` so the element never stretches
 *   to fill a flex/grid column. Inline-block still respects text flow when
 *   used inside a paragraph.
 *
 * Callers can override `bg`, `c`, `w`, or pass `style` for one-off cases
 * (e.g. event chips that need a transparent inner Code).
 */
export default function Code(props) {
  return (
    <MantineCode
      bg="indigo.1"
      c="indigo.9"
      display="inline-block"
      w="fit-content"
      {...props}
    />
  )
}

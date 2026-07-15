import { NumberInput as MantineNumberInput } from '@mantine/core'

/**
 * Numeric input with optional min/max/step.
 *
 * Usage:
 *   <NumberInput label="Approvals" min={1} value={n} onChange={setN} />
 */
export default function NumberInput(props) {
  return <MantineNumberInput radius="sm" {...props} />
}

import { Switch as MantineSwitch } from '@mantine/core'

/**
 * Toggle switch for boolean settings.
 *
 * Usage:
 *   <Switch label="Enable integration" checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} />
 */
export default function Switch(props) {
  return <MantineSwitch {...props} />
}

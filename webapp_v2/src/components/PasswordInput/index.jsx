import { PasswordInput as MantinePasswordInput } from '@mantine/core'

/**
 * Password / secret input with visibility toggle.
 *
 * Usage:
 *   <PasswordInput label="API Token" value={token} onChange={(e) => setToken(e.currentTarget.value)} />
 */
export default function PasswordInput(props) {
  return <MantinePasswordInput radius="sm" {...props} />
}

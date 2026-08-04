import { Avatar } from '@mantine/core'
import { initialsFor } from '../utils'

// Second consumer: Wave 6 session details header (B6.1).

/**
 * Port of `webapp.components.user-icon/initials-black` — first letter of the
 * first and last name tokens, in a dark circle. The nil-safety lives in
 * `initialsFor`/`displayNameFor` (see ../utils).
 */
export default function UserAvatar({ name }) {
  return (
    <Avatar color="dark" variant="filled" radius="xl" size={32}>
      {initialsFor(name)}
    </Avatar>
  )
}

import { Box } from '@mantine/core'
import { getUserInitials } from '@/utils/user'
import classes from './Header.module.css'

// Not Mantine's <Avatar>: the established visual is a bordered circle filled
// with the body colour and 11px/700 initials, which Avatar does not produce.
// aria-hidden because the button that wraps it carries the accessible name.
export function UserAvatar({ user }) {
  return (
    <Box aria-hidden="true" className={classes.avatar}>
      {getUserInitials(user)}
    </Box>
  )
}

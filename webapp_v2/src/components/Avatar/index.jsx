import { Avatar as MantineAvatar } from '@mantine/core'
import classes from './Avatar.module.css'

// Round by default. `tile` makes it an icon tile: its corners come from
// --hoop-icon-radius (theme.js), so a page never passes a radius itself.
function Avatar({ radius = 'xl', tile = false, ...props }) {
  return (
    <MantineAvatar
      radius={tile ? 'var(--hoop-icon-radius)' : radius}
      {...props}
      classNames={{ root: classes.root }}
    />
  )
}

function Group({ spacing = 6, ...props }) {
  return <MantineAvatar.Group spacing={spacing} {...props} />
}

Avatar.Group = Group

export default Avatar

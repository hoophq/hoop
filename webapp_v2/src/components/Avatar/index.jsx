import { Avatar as MantineAvatar } from '@mantine/core'
import classes from './Avatar.module.css'

// Inside a Group each avatar gets a 1px ring in the body colour so the overlap
// reads as a stack. Mantine's own group border is 2px, which at 24px eats a
// third of the icon.
function Avatar({ radius = 'xl', classNames = {}, ...props }) {
  return <MantineAvatar radius={radius} classNames={{ root: classes.root, ...classNames }} {...props} />
}

function Group({ spacing = 8, ...props }) {
  return <MantineAvatar.Group spacing={spacing} {...props} />
}

Avatar.Group = Group

export default Avatar

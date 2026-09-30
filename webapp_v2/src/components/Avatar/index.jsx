import { Avatar as MantineAvatar } from '@mantine/core'
import classes from './Avatar.module.css'

function Avatar({ radius = 'xl', ...props }) {
  return <MantineAvatar radius={radius} {...props} classNames={{ root: classes.root }} />
}

function Group({ spacing = 6, ...props }) {
  return <MantineAvatar.Group spacing={spacing} {...props} />
}

Avatar.Group = Group

export default Avatar

import { Avatar as MantineAvatar } from '@mantine/core'
import classes from './Avatar.module.css'

function Avatar({ radius = 'xl', classNames = {}, ...props }) {
  return <MantineAvatar radius={radius} classNames={{ root: classes.root, ...classNames }} {...props} />
}

function Group({ spacing = 6, ...props }) {
  return <MantineAvatar.Group spacing={spacing} {...props} />
}

Avatar.Group = Group

export default Avatar

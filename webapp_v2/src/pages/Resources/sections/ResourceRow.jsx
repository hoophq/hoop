import { useNavigate } from 'react-router-dom'
import { Group, Image, Text } from '@mantine/core'
import Button from '@/components/Button'
import classes from './List.module.css'

export default function ResourceRow({ resource, iconSrc, iconAlt, isAdmin }) {
  const navigate = useNavigate()
  return (
    <li className={classes.row}>
      <Group gap="md" wrap="nowrap">
        <Image src={iconSrc} alt={iconAlt} w={24} h={24} fit="contain" loading="lazy" />
        <Text component="h3" fw={500}>
          {resource.name}
        </Text>
      </Group>
      {isAdmin && (
        <Button
          variant="light"
          size="sm"
          color="gray"
          aria-label={'Configure ' + resource.name}
          onClick={() => navigate('/resources/configure/' + encodeURIComponent(resource.name))}
        >
          Configure
        </Button>
      )}
    </li>
  )
}

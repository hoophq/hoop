import { Group, Image, Paper, Stack, Text, UnstyledButton } from '@mantine/core'
import { ChevronRight } from 'lucide-react'
import Avatar from '@/components/Avatar'
import classes from './ActionCard.module.css'

/**
 * A card that runs one action or opens one page: icon, title, description and
 * a trailing chevron.
 *
 * - icon:  lucide component (mutually exclusive with `image`)
 * - image: logo `src` for the same slot
 * - badge: node next to the title
 */
export default function ActionCard({ icon: Icon, image, title, badge, description, disabled, onClick }) {
  return (
    <UnstyledButton onClick={onClick} disabled={disabled} className={classes.card}>
      <Paper withBorder p="md" bg="transparent">
        <Group gap="md" wrap="nowrap">
          <Avatar size="lg" tile color="gray" variant="light">
            {image ? <Image src={image} alt="" w={24} h={24} fit="contain" /> : <Icon size={20} />}
          </Avatar>
          <Stack gap={2} flex={1}>
            <Group gap="xs">
              <Text fw={500}>{title}</Text>
              {badge}
            </Group>
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          </Stack>
          <ChevronRight size={16} aria-hidden="true" />
        </Group>
      </Paper>
    </UnstyledButton>
  )
}

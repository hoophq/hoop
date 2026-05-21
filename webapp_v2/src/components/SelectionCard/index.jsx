import { Group, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import classes from './SelectionCard.module.css'

function SelectionCard({ icon: Icon, title, description, selected, onClick }) {
  return (
    <UnstyledButton
      p="md"
      onClick={onClick}
      className={classes.card}
      data-selected={selected || undefined}
      aria-pressed={!!selected}
    >
      <Group gap="md" align="center" wrap="nowrap">
        {Icon && (
          <ThemeIcon size="lg" radius="md" variant="default" className={classes.icon}>
            <Icon size={20} aria-hidden="true" />
          </ThemeIcon>
        )}
        <Stack gap={2} align="flex-start">
          <Text size="sm" fw={500} c={selected ? 'white' : undefined}>
            {title}
          </Text>
          {description && (
            <Text size="xs" ta="left" c={selected ? 'rgba(255,255,255,0.7)' : 'dimmed'}>
              {description}
            </Text>
          )}
        </Stack>
      </Group>
    </UnstyledButton>
  )
}

export default SelectionCard

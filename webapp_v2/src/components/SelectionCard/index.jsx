import { Group, Image, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import classes from './SelectionCard.module.css'

/**
 * Selectable card with a leading glyph, a title and an optional description.
 *
 * The glyph sits in a soft gray wrapper, matching the CLJS `Avatar
 * variant="soft" color="gray"` this is a port of.
 *
 * Props:
 * - icon:   lucide component rendered at 20px (mutually exclusive with `image`)
 * - image:  logo `src` rendered in the same slot; flattened to white when the
 *           card is selected, since the selected surface is dark
 * - badge:  node rendered next to the title (e.g. a "Recommended" pill)
 */
function SelectionCard({ icon: Icon, image, badge, title, description, selected, onClick }) {
  return (
    <UnstyledButton
      p="md"
      onClick={onClick}
      className={classes.card}
      data-selected={selected || undefined}
      aria-pressed={!!selected}
    >
      <Group gap="md" align="center" wrap="nowrap">
        {(image || Icon) && (
          <ThemeIcon
            size="lg"
            radius="md"
            variant="light"
            color="gray"
            className={classes.icon}
          >
            {image ? (
              <Image
                src={image}
                alt=""
                w={20}
                h={20}
                fit="contain"
                className={classes.logo}
              />
            ) : (
              <Icon size={20} aria-hidden="true" />
            )}
          </ThemeIcon>
        )}
        <Stack gap={2} align="flex-start">
          <Group gap="xs" align="center" wrap="nowrap">
            <Text size="sm" fw={500} c={selected ? 'white' : undefined}>
              {title}
            </Text>
            {badge}
          </Group>
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

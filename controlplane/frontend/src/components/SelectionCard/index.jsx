import { Box, Group, Image, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import classes from './SelectionCard.module.css'

/**
 * Selectable card with a leading glyph, a title and an optional description.
 *
 * - icon:  lucide component at 20px (mutually exclusive with `image`)
 * - image: logo `src` for the same slot
 * - badge: node pinned to the trailing edge, e.g. a "Recommended" pill
 */
function SelectionCard({
  icon: Icon,
  image,
  badge,
  title,
  description,
  selected,
  disabled,
  onClick,
}) {
  return (
    <UnstyledButton
      p="md"
      onClick={onClick}
      disabled={disabled}
      className={classes.card}
      data-selected={selected || undefined}
      aria-pressed={!!selected}
    >
      <Group justify="space-between" align="center" wrap="nowrap" gap="md">
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
        {badge && <Box className={classes.badge}>{badge}</Box>}
      </Group>
    </UnstyledButton>
  )
}

export default SelectionCard

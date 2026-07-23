import { Group, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import Badge from '@/components/Badge'
import Radio from '@/components/Radio'
import classes from './ProfileCard.module.css'

/**
 * Selectable protection-profile card: icon, title, optional badge, radio
 * indicator, and either a bullet list (compliance profiles) or a short
 * description (general purpose / manual).
 *
 * Rendered as a button with role="radio" — the visual radio is an input-less
 * Radio.Indicator, so the markup stays valid.
 */
function ProfileCard({ profile, selected, disabled, onSelect }) {
  const { title, icon: Icon, iconColor, iconVariant, badge, bullets, description } = profile

  return (
    <UnstyledButton
      p="md"
      className={classes.card}
      role="radio"
      aria-checked={!!selected}
      data-selected={selected || undefined}
      disabled={disabled}
      onClick={onSelect}
    >
      <Group gap="md" align="flex-start" wrap="nowrap">
        <ThemeIcon size="xl" radius="md" color={iconColor} variant={iconVariant}>
          <Icon size={20} aria-hidden="true" />
        </ThemeIcon>

        <Stack gap="xs" flex={1}>
          <Group gap="sm" wrap="nowrap">
            <Text fw={600}>{title}</Text>
            {badge && (
              <Badge variant="light" color={badge.color} size="sm" tt="none" ml="auto">
                {badge.label}
              </Badge>
            )}
          </Group>

          {bullets && (
            <Stack gap={4}>
              {bullets.map((item) => (
                <Text key={item} size="xs" c="dimmed">
                  {`• ${item}`}
                </Text>
              ))}
            </Stack>
          )}

          {description && (
            <Text size="sm" c="dimmed" ta="left">
              {description}
            </Text>
          )}
        </Stack>

        <Radio.Indicator checked={!!selected} disabled={disabled} size="sm" />
      </Group>
    </UnstyledButton>
  )
}

export default ProfileCard

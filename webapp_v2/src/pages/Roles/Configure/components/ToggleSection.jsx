import { Group, Stack, Title, Text, Box } from '@mantine/core'
import Switch from '@/components/Switch'

// Reusable toggle row used across Terminal Access and Native Access.
//
// Layout intent (matches CLJS Flex {:align "center"}): the Switch sits
// vertically centered with the title + description block. Optional
// `complement` (extra inputs that appear when the toggle is on) and
// `learnMore` (docs link) render BELOW the centered group so a long
// complement never tugs the switch off-center.
export default function ToggleSection({
  title,
  description,
  checked,
  disabled,
  onChange,
  complement,
  learnMore,
}) {
  return (
    <Stack gap="sm">
      <Group align="center" gap="md" wrap="nowrap">
        <Switch
          size="md"
          checked={checked}
          disabled={disabled}
          onChange={(e) => onChange(e.currentTarget.checked)}
          aria-label={title}
        />
        <Stack gap="xs" flex={1}>
          <Title order={5} fw={500}>{title}</Title>
          <Text size="sm" c="dimmed">{description}</Text>
        </Stack>
      </Group>
      {complement && <Box>{complement}</Box>}
      {learnMore && <Box>{learnMore}</Box>}
    </Stack>
  )
}

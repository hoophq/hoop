import { Grid, Group, Stack, Text, Title } from '@mantine/core'

/**
 * Two-column form row: the heading and its supporting copy on the left, the
 * fields on the right.
 *
 * This is the app's form layout. Sixteen form pages put their fields straight
 * on the page background — none of them wraps inputs in a bordered container —
 * and ten lay them out on this 2/5 grid. Putting the explanation in the left
 * column is what lets each field drop its own `description` line, which is most
 * of what makes a long form read as a wall.
 *
 * It was copied into ten pages before it was a component. This is the copy they
 * should collapse onto; `pages/Features/AiSessionAnalyzer/components/SectionRow`
 * already re-exports it, and the rest is a mechanical change of its own.
 */
export default function SectionRow({ title, badge, description, callout, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Group gap="xs" align="center">
            <Title order={4} fw={500}>
              {title}
            </Title>
            {badge}
          </Group>
          {description && (
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          )}
          {callout}
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}

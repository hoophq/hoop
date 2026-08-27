import { Grid, Group, Stack, Text, Title } from '@mantine/core'

// Two-column form row: heading and supporting copy on the left, the fields on
// the right. Shared by the Configure tab and the rule form.
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
          <Text size="sm" c="dimmed">
            {description}
          </Text>
          {callout}
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}

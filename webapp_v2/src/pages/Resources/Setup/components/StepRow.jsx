import { Grid, Stack, Text, Title } from '@mantine/core'

// The CLJS wizard row: heading and copy on 3 of 7 columns, the fields on 4.
export default function StepRow({ title, description, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={{ base: 7, sm: 3 }}>
        <Stack gap={4}>
          <Title order={4} fz="lg" fw={700}>
            {title}
          </Title>
          <Text size="sm" c="dimmed">
            {description}
          </Text>
        </Stack>
      </Grid.Col>
      <Grid.Col span={{ base: 7, sm: 4 }}>{children}</Grid.Col>
    </Grid>
  )
}

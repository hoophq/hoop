import { Stack, Text, Button, Image, Anchor } from '@mantine/core'

export default function EmptyState({ title, description, action, docsUrl, docsLabel }) {
  return (
    <Stack flex={1} mih="50vh" align="center" py="xxl">
      <Stack flex={1} align="center" justify="center" gap="xl">
        <Image
          src="/images/illustrations/empty-state.png"
          alt=""
          w={320}
          fit="contain"
        />
        <Stack align="center" gap="xs" maw={400}>
          <Text fw={600} c="dimmed" ta="center">{title}</Text>
          {description && (
            <Text size="sm" c="dimmed" ta="center">{description}</Text>
          )}
        </Stack>
        {action && (
          <Button onClick={action.onClick}>{action.label}</Button>
        )}
      </Stack>

      {docsUrl && docsLabel && (
        <Text mt="auto" size="sm" c="dimmed" ta="center">
          {'Need more information? Check out our '}
          <Anchor href={docsUrl} target="_blank" size="sm">
            {docsLabel}
          </Anchor>
          {'.'}
        </Text>
      )}
    </Stack>
  )
}

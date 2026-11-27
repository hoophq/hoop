import { Image, Stack, Text, Title } from '@mantine/core'

// The page the Slack app opens after it links a user or a workspace.
export default function SlackResult({ image, title, description }) {
  return (
    <Stack align="center" gap="lg" py="xxlAlt">
      <Image src={image} alt="" w={320} fit="contain" />
      <Stack align="center" gap="xs">
        <Title order={2} ta="center">
          {title}
        </Title>
        <Text c="dimmed" ta="center">
          {description}
        </Text>
      </Stack>
    </Stack>
  )
}

import { Stack, Text, Title } from '@mantine/core'
import { useNavigate } from 'react-router-dom'
import Button from '@/components/Button'

// Illustration-free on purpose: every image in the app is served from public/, and a
// 404 page that itself renders a broken asset is not much of a 404 page.
export default function NotFound() {
  const navigate = useNavigate()

  return (
    <Stack mih="100vh" align="center" justify="center" gap="lg" p="xl">
      <Stack align="center" gap="xs" maw={460}>
        <Title order={1}>404</Title>
        <Text fw={600}>Page not found</Text>
        <Text size="sm" c="dimmed" ta="center">
          This page does not exist in the control plane.
        </Text>
      </Stack>
      <Button onClick={() => navigate('/')}>Go to the start page</Button>
    </Stack>
  )
}

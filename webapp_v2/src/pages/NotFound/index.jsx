import { Stack, Text, Title } from '@mantine/core'
import { useNavigate } from 'react-router-dom'
import Button from '@/components/Button'

// Rendered inside the shell by modes/ModeCatchAll. Illustration-free on
// purpose: a 404 page that itself renders a broken asset is not much of one.
export default function NotFound() {
  const navigate = useNavigate()

  return (
    <Stack mih="60vh" align="center" justify="center" gap="lg" p="xl">
      <Stack align="center" gap="xs" maw={460}>
        <Title order={1}>404</Title>
        <Text fw={600}>Page not found</Text>
        <Text size="sm" c="dimmed" ta="center">
          This page does not exist.
        </Text>
      </Stack>
      <Button onClick={() => navigate('/')}>Go to the start page</Button>
    </Stack>
  )
}

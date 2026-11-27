import { useNavigate } from 'react-router-dom'
import { Box, Group, Image, Stack } from '@mantine/core'
import Button from '@/components/Button'
import { useAuthStore } from '@/stores/useAuthStore'

export function OnboardingLogo() {
  return <Image src="/images/hoop-branding/PNG/hoop-symbol_black@4x.png" alt="Hoop" w={40} h={40} fit="contain" />
}

export function LogoutButton() {
  const navigate = useNavigate()
  const logout = useAuthStore((s) => s.logout)
  return (
    <Button
      variant="subtle"
      color="gray"
      onClick={() => {
        logout()
        navigate('/login')
      }}
    >
      Log out
    </Button>
  )
}

// The onboarding has no sidebar and no header, as the :auth layout in CLJS.
export default function OnboardingLayout({ logout = false, children }) {
  return (
    <Stack mih="100dvh" gap={0} bg="white">
      {logout && (
        <Group justify="flex-end" px="xl" pt="xl">
          <LogoutButton />
        </Group>
      )}
      <Box flex={1} p="xl">
        {children}
      </Box>
    </Stack>
  )
}

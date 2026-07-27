import { useEffect, useState } from 'react'
import { Center, Stack, Loader, Text, Transition } from '@mantine/core'
import { XCircle } from 'lucide-react'

// Full-screen loading state for auth-flow routes (login redirect, OAuth
// callbacks, session verification). Auth screens are always dark — there is
// no light variant — so the dark styling is baked in rather than passed in.
function AuthPageLoader({ message, description, error }) {
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    const timer = setTimeout(() => setVisible(true), 50)
    return () => clearTimeout(timer)
  }, [])

  return (
    <Center h="100vh" bg="var(--sidebar-bg)">
      <Transition mounted={visible} transition="fade" duration={300}>
        {(styles) => (
          <Stack align="center" gap="xl" maw={320} style={styles}>
            <img
              src="/images/hoop-branding/SVG/hoop-symbol_white.svg"
              height={40}
              width={40}
              alt="hoop"
            />

            {error ? (
              <XCircle size={32} color="var(--mantine-color-red-6)" strokeWidth={1.5} />
            ) : (
              <Loader size="sm" type="dots" color="gray.0" />
            )}

            {(message || description) && (
              <Stack align="center" gap={6}>
                {message && (
                  <Text size="sm" c="gray.0" ta="center" fw={500}>
                    {message}
                  </Text>
                )}
                {description && (
                  <Text size="xs" c="gray.5" ta="center">
                    {description}
                  </Text>
                )}
              </Stack>
            )}
          </Stack>
        )}
      </Transition>
    </Center>
  )
}

export default AuthPageLoader

import { useEffect, useState } from 'react'
import { Center, Stack, Loader, Text, Transition } from '@mantine/core'
import { XCircle } from 'lucide-react'

function PageLoader({ message, description, error, overlay }) {
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    const timer = setTimeout(() => setVisible(true), 50)
    return () => clearTimeout(timer)
  }, [])

  const containerStyle = overlay
    ? {
        position: 'fixed',
        inset: 0,
        backgroundColor: 'var(--mantine-color-body)',
        zIndex: 200,
      }
    : { height: '100vh' }

  return (
    <Center style={containerStyle}>
      <Transition mounted={visible} transition="fade" duration={300}>
        {(styles) => (
          <Stack align="center" gap="xl" style={{ ...styles, maxWidth: 320 }}>
            <Text
              style={{
                fontSize: 22,
                fontWeight: 700,
                letterSpacing: '-0.5px',
                color: 'var(--mantine-color-indigo-8)',
                lineHeight: 1,
              }}
            >
              hoop
            </Text>

            {error ? (
              <XCircle size={32} color="var(--mantine-color-red-6)" strokeWidth={1.5} />
            ) : (
              <Loader size="sm" type="dots" color="indigo" />
            )}

            {(message || description) && (
              <Stack align="center" gap={6}>
                {message && (
                  <Text size="sm" c="dimmed" ta="center" fw={500}>
                    {message}
                  </Text>
                )}
                {description && (
                  <Text size="xs" c="dimmed" ta="center" style={{ opacity: 0.7 }}>
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

export default PageLoader

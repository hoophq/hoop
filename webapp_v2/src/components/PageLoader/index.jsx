import { useEffect, useState } from 'react'
import { Center, Stack, Loader, Text, Transition } from '@mantine/core'
import { XCircle } from 'lucide-react'

function PageLoader({ message, description, error, overlay, h }) {
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
    : undefined

  return (
    <Center style={containerStyle} h={overlay ? undefined : (h ?? '100vh')}>
      <Transition mounted={visible} transition="fade" duration={300}>
        {(styles) => (
          <Stack align="center" gap="xl" style={{ ...styles, maxWidth: 320 }}>
            <img
              src="/images/hoop-branding/SVG/hoop-symbol_black.svg"
              height={40}
              width={40}
              alt="hoop"
            />

            {error ? (
              <XCircle size={32} color="var(--mantine-color-red-6)" strokeWidth={1.5} />
            ) : (
              <Loader size="sm" type="dots" color="dark" />
            )}

            {(message || description) && (
              <Stack align="center" gap={6}>
                {message && (
                  <Text size="sm" c="dimmed" ta="center" fw={500}>
                    {message}
                  </Text>
                )}
                {description && (
                  <Text size="xs" c="dimmed" ta="center">
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

import { useEffect, useState } from 'react'
import { Center, Stack, Loader, Text, Transition } from '@mantine/core'
import { XCircle } from 'lucide-react'

// A loader replaces the whole page, so it fills the whole page. Two things sit
// above it and both are subtracted here, because getting either wrong is
// visible: too tall flashes a scrollbar, too short parks the spinner near the
// top of an empty screen instead of in the middle of it.
//
//   --app-shell-header-offset  Mantine's global header, reserved by AppShell.Main
//   --hoop-page-padding        the padding PageLayout puts around every page
//
// Both are absent outside the shell — the auth routes render bare — where they
// fall back to zero and this is a plain viewport height, which is what those
// pages want.
//
// Do NOT pass `h` to scope this to a box. A page whose content has not loaded
// has no size to respect, and `h={300}` is how you get a spinner floating in
// the top third of the screen.
const DEFAULT_HEIGHT =
  'calc(100dvh - var(--app-shell-header-offset, 0rem) - var(--hoop-page-padding, 0rem) * 2)'

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
    <Center style={containerStyle} h={overlay ? undefined : (h ?? DEFAULT_HEIGHT)}>
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

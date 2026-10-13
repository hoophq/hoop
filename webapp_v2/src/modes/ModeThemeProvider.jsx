import { MantineProvider } from '@mantine/core'
import { useModeConfig } from '@/modes'

// The MantineProvider, fed by the mode's theme slot (src/theme.js).
export default function ModeThemeProvider({ children }) {
  const { theme } = useModeConfig()
  return (
    <MantineProvider
      theme={theme.theme}
      defaultColorScheme="light"
      cssVariablesResolver={theme.cssVariablesResolver}
    >
      {children}
    </MantineProvider>
  )
}

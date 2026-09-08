import { MantineProvider } from '@mantine/core'
import { useModeConfig } from '@/modes'

// The MantineProvider, fed by the mode's theme slot. Both modes share
// src/theme.js today, so nothing can flash while /publicserverinfo is in
// flight. When a mode gets its own theme, decide here between accepting a
// sub-second gateway-themed login page and gating on useUserStore.appModeLoaded.
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

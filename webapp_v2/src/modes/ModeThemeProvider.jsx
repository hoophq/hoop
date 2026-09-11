import { MantineProvider } from '@mantine/core'
import { useModeConfig } from '@/modes'

// The MantineProvider, fed by the mode's theme slot. The control plane has its
// own theme (src/theme.controlPlane.js, a softer disabled state); the gateway
// keeps src/theme.js.
//
// appMode is 'gateway' until /publicserverinfo answers, so a control plane boot
// paints one frame with the gateway's tokens. That is accepted rather than
// gated on useUserStore.appModeLoaded: the two themes differ by three disabled
// colors, and holding the tree back would cost every boot of both products a
// blank frame to spare a flicker only a disabled control on screen can show.
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

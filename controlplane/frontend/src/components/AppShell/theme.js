import { AppShell } from '@mantine/core'

export const AppShellTheme = AppShell.extend({
  styles: {
    navbar: { transition: 'width 200ms ease' },
    main:   { transition: 'padding-left 200ms ease' },
  },
})

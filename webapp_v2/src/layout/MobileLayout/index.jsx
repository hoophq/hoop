import { AppShell } from '@mantine/core'
import { Outlet } from 'react-router-dom'
import MobileTabBar from './MobileTabBar'
import { TAB_BAR_HEIGHT } from './constants'

// The footer absorbs the iOS home-indicator inset in standalone (PWA) mode.
// env() resolves to 0 on devices without a safe area; requires the
// viewport-fit=cover meta set in index.html. The tab bar itself stays
// TAB_BAR_HEIGHT tall, anchored at the top of the footer.
const FOOTER_HEIGHT = `calc(${TAB_BAR_HEIGHT}px + env(safe-area-inset-bottom))`

/**
 * App shell for the Mobile Admin PWA (/m/*): content area + bottom tab bar.
 * Sibling of the desktop Layout — never mounted on desktop routes.
 */
function MobileLayout() {
  return (
    <AppShell footer={{ height: FOOTER_HEIGHT }} padding="md">
      <AppShell.Main>
        <Outlet />
      </AppShell.Main>
      <AppShell.Footer>
        <MobileTabBar />
      </AppShell.Footer>
    </AppShell>
  )
}

export default MobileLayout
